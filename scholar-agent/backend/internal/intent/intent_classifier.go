package intent

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

// Engineer 意图识别引擎 - 实现三级匹配 + 竞争式并行处理
type Engineer struct {
	// 规则匹配器（优先级1）
	ruleMatcher *RuleMatcher
	// 向量匹配器（优先级2）
	vectorMatcher *VectorMatcher
	// LLM 意图推理器（优先级3）
	llmInferrer *LLMIntentInferrer
	// 模式状态管理器
	stateManager *StateManager
	// 内存缓存
	cache *MemoryCache
	// Milvus 客户端
	milvusClient *milvusclient.Client
}

// NewEngineer 创建意图识别引擎实例
func NewEngineer(ctx context.Context, cfg EngineConfig) (*Engineer, error) {
	// 初始化内存缓存
	cache := NewMemoryCache()

	// 初始化规则匹配器
	ruleMatcher := NewRuleMatcher()

	// 初始化模式状态管理器
	stateManager := NewStateManager(cache)

	eng := &Engineer{
		ruleMatcher:  ruleMatcher,
		stateManager: stateManager,
		cache:        cache,
	}

	// 初始化 Milvus 客户端（可选，连接失败不影响规则匹配和 LLM 推理）
	if cfg.MilvusURL != "" {
		milvusClient, err := NewMilvusClient(ctx, cfg.MilvusURL)
		if err != nil {
			log.Printf("[Engineer] Milvus 连接失败，向量匹配将不可用: %v", err)
		} else {
			eng.milvusClient = milvusClient

			// 确保集合存在
			if err := EnsureCollection(ctx, milvusClient); err != nil {
				log.Printf("[Engineer] 创建 Milvus 集合失败: %v", err)
			}

			// 初始化向量匹配器
			vm, err := NewVectorMatcher(milvusClient, VectorMatcherConfig{
				APIKey:  cfg.EmbeddingAPIKey,
				BaseURL: cfg.EmbeddingURL,
				Model:   cfg.EmbeddingModel,
			})
			if err != nil {
				log.Printf("[Engineer] 向量匹配器初始化失败: %v", err)
			} else {
				eng.vectorMatcher = vm
			}
		}
	}

	// 初始化 LLM 意图推理器（可选）
	if cfg.LLMAPIKey != "" {
		inferrer, err := NewLLMIntentInferrer(LLMIntentConfig{
			APIKey:  cfg.LLMAPIKey,
			BaseURL: cfg.LLMBaseURL,
			Model:   cfg.LLMModel,
		})
		if err != nil {
			log.Printf("[Engineer] LLM 意图推理器初始化失败: %v", err)
		} else {
			eng.llmInferrer = inferrer
		}
	}

	log.Printf("[Engineer] 意图识别引擎初始化完成 (Rule=%v, Vector=%v, LLM=%v)",
		eng.ruleMatcher != nil, eng.vectorMatcher != nil, eng.llmInferrer != nil)

	return eng, nil
}

// vectorResult 向量匹配 goroutine 的结果
type vectorResult struct {
	infos []IntentVectorInfo
	err   error
}

// llmResult LLM 推理 goroutine 的结果
type llmResult struct {
	info *IntentInfo
	err  error
}

// Recognize 识别用户意图 - 主入口方法
// 实现三级匹配: 规则匹配(同步) → 向量匹配+LLM推理(并行竞争)
func (eng *Engineer) Recognize(ctx context.Context, chatCtx *ChatContext) (*IntentResult, error) {
	startTime := time.Now()
	question := chatCtx.Question

	if question == "" {
		return &IntentResult{HitIntent: false, ToolName: "none", MatchSource: MatchSourceNone}, nil
	}

	// 填充上一轮对话记录
	if chatCtx.LastHistoryData == "" {
		chatCtx.LastHistoryData = eng.stateManager.GetLastHistory(chatCtx.UserId, chatCtx.DeviceId)
	}

	// Step0: 检查是否已在特定模式中
	currentMode, inMode := eng.stateManager.GetCurrentMode(chatCtx.UserId, chatCtx.DeviceId)
	if inMode {
		result, handled := eng.handleModeInternal(ctx, chatCtx, currentMode)
		if handled {
			log.Printf("[Engineer] 模式内处理完成: mode=%s, elapsed=%v", currentMode, time.Since(startTime))
			return result, nil
		}
	}

	// Step1: 规则匹配（最快，同步执行）
	if kw, hit := eng.ruleMatcher.Match(question); hit {
		result := &IntentResult{
			HitIntent:   true,
			ToolName:    kw.Type,
			Keyword:     kw.Keyword,
			MatchSource: MatchSourceRule,
			IntentType:  MapToIntentType(kw.Type),
		}

		// 检查是否需要进入模式
		eng.tryEnterMode(chatCtx, kw.Type)

		log.Printf("[Engineer] 规则匹配命中: toolName=%s, elapsed=%v", kw.Type, time.Since(startTime))
		return result, nil
	}

	// Step2: 并行提交向量匹配和LLM推理（竞争式等待）
	result := eng.parallelRecognize(ctx, chatCtx)

	// 检查是否需要进入模式
	if result.HitIntent && result.ToolName != "none" && result.ToolName != "chat" {
		eng.tryEnterMode(chatCtx, result.ToolName)
	}

	log.Printf("[Engineer] 意图识别完成: toolName=%s, source=%s, elapsed=%v",
		result.ToolName, result.MatchSource, time.Since(startTime))

	return result, nil
}

// parallelRecognize 并行执行向量匹配和LLM推理，竞争式等待
func (eng *Engineer) parallelRecognize(ctx context.Context, chatCtx *ChatContext) *IntentResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	vectorCh := make(chan vectorResult, 1)
	llmCh := make(chan llmResult, 1)

	// 启动向量匹配任务
	if eng.vectorMatcher != nil {
		go func() {
			infos, err := eng.vectorMatcher.Search(ctx, chatCtx.Question, "")
			vectorCh <- vectorResult{infos: infos, err: err}
		}()
	} else {
		vectorCh <- vectorResult{} // 向量匹配不可用，直接返回空
	}

	// 启动 LLM 推理任务
	if eng.llmInferrer != nil {
		go func() {
			info, err := eng.llmInferrer.Infer(ctx, chatCtx)
			llmCh <- llmResult{info: info, err: err}
		}()
	} else {
		llmCh <- llmResult{info: &IntentInfo{ToolName: "none"}} // LLM 不可用
	}

	// 竞争式等待
	var vecResult vectorResult
	var llmRes llmResult
	vecDone := false
	llmDone := false

	for !vecDone || !llmDone {
		select {
		case vr := <-vectorCh:
			vecDone = true
			vecResult = vr
			// 向量匹配命中且有结果
			if vr.err == nil && len(vr.infos) > 0 {
				cancel() // 取消其他任务
				actionMsg := vr.infos[0].ActionMsg
				return &IntentResult{
					HitIntent:   true,
					ToolName:    actionMsg,
					MatchSource: MatchSourceVector,
					Score:       vr.infos[0].Score,
					VectorInfos: vr.infos,
					IntentType:  MapToIntentType(actionMsg),
				}
			}

		case lr := <-llmCh:
			llmDone = true
			llmRes = lr
			// LLM 推理命中且不为 none/chat
			if lr.err == nil && lr.info != nil && lr.info.ToolName != "none" && lr.info.ToolName != "chat" {
				cancel() // 取消其他任务
				return &IntentResult{
					HitIntent:   true,
					ToolName:    lr.info.ToolName,
					Keyword:     lr.info.Keyword,
					Keyword2:    lr.info.Keyword2,
					MatchSource: MatchSourceLLM,
					IntentType:  MapToIntentType(lr.info.ToolName),
				}
			}

		case <-ctx.Done():
			// 上下文取消
			return &IntentResult{HitIntent: false, ToolName: "none", MatchSource: MatchSourceNone, IntentType: "General"}
		}
	}

	// 都未命中有效意图，检查 LLM 是否返回了 chat
	if llmRes.info != nil && llmRes.info.ToolName == "chat" {
		return &IntentResult{
			HitIntent:   false,
			ToolName:    "chat",
			Keyword:     llmRes.info.Keyword,
			MatchSource: MatchSourceLLM,
			IntentType:  "General",
		}
	}

	// 检查向量结果中是否有低优先级命中
	if vecResult.err == nil && len(vecResult.infos) > 0 {
		return &IntentResult{
			HitIntent:   true,
			ToolName:    vecResult.infos[0].ActionMsg,
			MatchSource: MatchSourceVector,
			Score:       vecResult.infos[0].Score,
			VectorInfos: vecResult.infos,
			IntentType:  MapToIntentType(vecResult.infos[0].ActionMsg),
		}
	}

	// 完全未命中
	return &IntentResult{
		HitIntent:   false,
		ToolName:    "none",
		MatchSource: MatchSourceNone,
		IntentType:  "General",
	}
}

// handleModeInternal 模式内处理：检查退出 or 继续在当前模式中
func (eng *Engineer) handleModeInternal(ctx context.Context, chatCtx *ChatContext, currentMode CommandType) (*IntentResult, bool) {
	// 尝试向量匹配查看是否有 EXIT 指令
	var vectorInfos []IntentVectorInfo
	if eng.vectorMatcher != nil {
		infos, err := eng.vectorMatcher.Search(ctx, chatCtx.Question, string(currentMode))
		if err == nil {
			vectorInfos = infos
		}
	}

	continueInMode, shouldExit := eng.stateManager.HandleModeCheck(currentMode, vectorInfos)

	if shouldExit {
		eng.stateManager.ClearMode(chatCtx.UserId, chatCtx.DeviceId)
		return &IntentResult{
			HitIntent:   true,
			ToolName:    "none",
			MatchSource: MatchSourceVector,
			IntentType:  "General",
		}, true
	}

	if continueInMode {
		// 刷新模式过期时间
		eng.stateManager.RefreshMode(chatCtx.UserId, chatCtx.DeviceId, currentMode, CacheKeyIntentTypeTTL)

		// 根据当前模式确定意图
		toolName := modeToDefaultTool(currentMode)
		return &IntentResult{
			HitIntent:   true,
			ToolName:    toolName,
			MatchSource: MatchSourceNone,
			IntentType:  MapToIntentType(toolName),
		}, true
	}

	return nil, false
}

// tryEnterMode 尝试根据意图动作进入对应模式
func (eng *Engineer) tryEnterMode(chatCtx *ChatContext, toolName string) {
	if mode, ok := ModeEntryActions[toolName]; ok {
		eng.stateManager.SetMode(chatCtx.UserId, chatCtx.DeviceId, mode, CacheKeyIntentTypeTTL)
		log.Printf("[Engineer] 进入模式: %s (%s)", mode, GetModeDescription(mode))
	}
}

// modeToDefaultTool 将模式映射到默认工具名
func modeToDefaultTool(mode CommandType) string {
	switch mode {
	case CommandPaperReading:
		return "summarizePaper"
	case CommandWritingAssist:
		return "polishText"
	case CommandDataAnalysis:
		return "dataAnalysis"
	case CommandLiteratureReview:
		return "writeLiteratureReview"
	case CommandExperimentDesign:
		return "experimentDesign"
	case CommandFormulaDerive:
		return "formulaDerive"
	default:
		return "chat"
	}
}

// RecognizeSimple 简化版意图识别 - 仅使用问题文本，自动构建 ChatContext
func (eng *Engineer) RecognizeSimple(ctx context.Context, question string) (*IntentResult, error) {
	chatCtx := &ChatContext{
		Question: question,
		DeviceId: "default",
	}
	return eng.Recognize(ctx, chatCtx)
}

// Close 关闭引擎释放资源
func (eng *Engineer) Close() {
	if eng.cache != nil {
		eng.cache.Close()
	}
	if eng.milvusClient != nil {
		(*eng.milvusClient).Close(context.Background())
	}
	log.Printf("[Engineer] 意图识别引擎已关闭")
}

// GetStateManager 获取状态管理器（供外部使用）
func (eng *Engineer) GetStateManager() *StateManager {
	return eng.stateManager
}

// GetRuleMatcher 获取规则匹配器（供外部使用）
func (eng *Engineer) GetRuleMatcher() *RuleMatcher {
	return eng.ruleMatcher
}

// String 引擎信息
func (eng *Engineer) String() string {
	return fmt.Sprintf("Engineer{rule=%v, vector=%v, llm=%v}",
		eng.ruleMatcher != nil, eng.vectorMatcher != nil, eng.llmInferrer != nil)
}
