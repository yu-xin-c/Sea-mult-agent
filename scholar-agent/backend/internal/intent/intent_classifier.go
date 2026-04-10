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

	eng := &Engineer{
		ruleMatcher: ruleMatcher,
		cache:       cache,
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

				// 插入预训练种子数据（仅在集合为空时执行）
				if err := vm.SeedData(ctx); err != nil {
					log.Printf("[Engineer] 种子数据插入失败（不影响向量匹配功能）: %v", err)
				}
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
		chatCtx.LastHistoryData = eng.getLastHistory(chatCtx.UserId, chatCtx.DeviceId)
	}
	log.Printf("[Engineer] 开始意图识别: question=%q, hasVector=%v, hasLLM=%v", question, eng.vectorMatcher != nil, eng.llmInferrer != nil)

	// Step1: 规则匹配（最快，同步执行）
	if kw, hit := eng.ruleMatcher.Match(question); hit {
		result := &IntentResult{
			HitIntent:   true,
			ToolName:    kw.Type,
			Keyword:     kw.Keyword,
			MatchSource: MatchSourceRule,
			IntentType:  MapToIntentType(kw.Type),
		}

		log.Printf("[Engineer] 规则匹配命中: toolName=%s, elapsed=%v", kw.Type, time.Since(startTime))
		return result, nil
	}

	// Step2: 并行提交向量匹配和LLM推理（竞争式等待）
	result := eng.parallelRecognize(ctx, chatCtx)

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

	if eng.vectorMatcher != nil {
		log.Printf("[Engineer] 并行任务启动: VectorMatcher")
		go func() {
			start := time.Now()
			infos, err := eng.vectorMatcher.Search(ctx, chatCtx.Question, "")
			log.Printf("[Engineer] VectorMatcher 返回: err=%v, hits=%d, elapsed=%v", err, len(infos), time.Since(start))
			vectorCh <- vectorResult{infos: infos, err: err}
		}()
	} else {
		log.Printf("[Engineer] VectorMatcher 不可用，跳过")
		vectorCh <- vectorResult{}
	}

	if eng.llmInferrer != nil {
		log.Printf("[Engineer] 并行任务启动: LLMIntent")
		go func() {
			start := time.Now()
			info, err := eng.llmInferrer.Infer(ctx, chatCtx)
			toolName := "none"
			if info != nil {
				toolName = info.ToolName
			}
			log.Printf("[Engineer] LLMIntent 返回: err=%v, toolName=%s, elapsed=%v", err, toolName, time.Since(start))
			llmCh <- llmResult{info: info, err: err}
		}()
	} else {
		log.Printf("[Engineer] LLMIntent 不可用，跳过")
		llmCh <- llmResult{info: &IntentInfo{ToolName: "none"}}
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
			if vr.err != nil {
				log.Printf("[Engineer] VectorMatcher 失败: %v", vr.err)
			}
			if vr.err == nil && len(vr.infos) > 0 {
				cancel() // 取消其他任务
				actionMsg := vr.infos[0].ActionMsg
				log.Printf("[Engineer] 采用 Vector 结果: toolName=%s, score=%.4f", actionMsg, vr.infos[0].Score)
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
			if lr.err != nil {
				log.Printf("[Engineer] LLMIntent 失败: %v", lr.err)
			}
			if lr.err == nil && lr.info != nil && lr.info.ToolName != "none" && lr.info.ToolName != "chat" {
				cancel() // 取消其他任务
				log.Printf("[Engineer] 采用 LLM 结果: toolName=%s, keyword=%s, keyword2=%s", lr.info.ToolName, lr.info.Keyword, lr.info.Keyword2)
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
			log.Printf("[Engineer] 并行识别被取消: %v", ctx.Err())
			return &IntentResult{HitIntent: false, ToolName: "none", MatchSource: MatchSourceNone, IntentType: "General"}
		}
	}

	if llmRes.info != nil && llmRes.info.ToolName == "chat" {
		log.Printf("[Engineer] 未命中工具意图，回退 chat")
		return &IntentResult{
			HitIntent:   false,
			ToolName:    "chat",
			Keyword:     llmRes.info.Keyword,
			MatchSource: MatchSourceLLM,
			IntentType:  "General",
		}
	}

	if vecResult.err == nil && len(vecResult.infos) > 0 {
		log.Printf("[Engineer] 采用 Vector 兜底结果: toolName=%s, score=%.4f", vecResult.infos[0].ActionMsg, vecResult.infos[0].Score)
		return &IntentResult{
			HitIntent:   true,
			ToolName:    vecResult.infos[0].ActionMsg,
			MatchSource: MatchSourceVector,
			Score:       vecResult.infos[0].Score,
			VectorInfos: vecResult.infos,
			IntentType:  MapToIntentType(vecResult.infos[0].ActionMsg),
		}
	}

	log.Printf("[Engineer] 全部识别链路未命中，返回 none")
	return &IntentResult{
		HitIntent:   false,
		ToolName:    "none",
		MatchSource: MatchSourceNone,
		IntentType:  "General",
	}
}

// getLastHistory 获取上一轮对话记录
func (eng *Engineer) getLastHistory(userId int64, deviceId string) string {
	key := LastHistoryKey(userId, deviceId)
	val, ok := eng.cache.GetString(key)
	if !ok {
		return ""
	}
	return val
}

// SetLastHistory 保存上一轮对话记录
func (eng *Engineer) SetLastHistory(userId int64, deviceId string, history string) {
	key := LastHistoryKey(userId, deviceId)
	eng.cache.Set(key, history, CacheKeyLastHistoryTTL)
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

// GetRuleMatcher 获取规则匹配器（供外部使用）
func (eng *Engineer) GetRuleMatcher() *RuleMatcher {
	return eng.ruleMatcher
}

// String 引擎信息
func (eng *Engineer) String() string {
	return fmt.Sprintf("Engineer{rule=%v, vector=%v, llm=%v}",
		eng.ruleMatcher != nil, eng.vectorMatcher != nil, eng.llmInferrer != nil)
}
