package intent

import (
	"context"
	"fmt"
	"log"
	"unicode"

	"github.com/cloudwego/eino-ext/components/embedding/openai"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

const (
	// CollectionName Milvus 集合名称
	CollectionName = "scholar_intent_vectors"
	// VectorDim 向量维度（与 embedding 模型输出一致）
	VectorDim = 1536
	// DefaultTopK 默认检索返回数量
	DefaultTopK = 10
	// ThresholdChinese 中文阈值（越小越相似）
	ThresholdChinese = float32(0.13)
	// ThresholdEnglish 英文阈值
	ThresholdEnglish = float32(0.20)
)

// VectorMatcher 向量匹配器 - 基于 Milvus 和 Eino Embedding 的语义相似度匹配
type VectorMatcher struct {
	milvusClient *milvusclient.Client
	embedder     *openai.Embedder
}

// VectorMatcherConfig 向量匹配器配置
type VectorMatcherConfig struct {
	APIKey  string
	BaseURL string
	Model   string
}

// NewVectorMatcher 创建向量匹配器实例
func NewVectorMatcher(milvusClient *milvusclient.Client, cfg VectorMatcherConfig) (*VectorMatcher, error) {
	if cfg.Model == "" {
		cfg.Model = "text-embedding-3-small"
	}

	embeddingCfg := &openai.EmbeddingConfig{
		APIKey: cfg.APIKey,
		Model:  cfg.Model,
	}
	if cfg.BaseURL != "" {
		embeddingCfg.BaseURL = cfg.BaseURL
	}

	embedder, err := openai.NewEmbedder(context.Background(), embeddingCfg)
	if err != nil {
		return nil, fmt.Errorf("初始化 Embedding 模型失败: %w", err)
	}

	return &VectorMatcher{
		milvusClient: milvusClient,
		embedder:     embedder,
	}, nil
}

// Search 执行向量相似度搜索
// content: 用户输入文本
// actionType: 可选的动作类型过滤
// 返回匹配到的意图向量信息列表
func (vm *VectorMatcher) Search(ctx context.Context, content string, actionType string) ([]IntentVectorInfo, error) {
	if content == "" {
		return nil, nil
	}

	// Step1: 文本向量化
	vectors, err := vm.embedder.EmbedStrings(ctx, []string{content})
	if err != nil {
		log.Printf("[VectorMatcher] Embedding 调用失败: %v", err)
		return nil, fmt.Errorf("文本向量化失败: %w", err)
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("Embedding 返回空向量")
	}

	// 转换为 float32
	queryVector := make([]float32, len(vectors[0]))
	for i, v := range vectors[0] {
		queryVector[i] = float32(v)
	}

	// Step2: 构建搜索请求
	searchVectors := []entity.Vector{entity.FloatVector(queryVector)}

	searchOpt := milvusclient.NewSearchOption(CollectionName, DefaultTopK, searchVectors)
	searchOpt.WithOutputFields("content", "action_type", "action_msg", "type", "tenant_id")

	if actionType != "" {
		searchOpt.WithFilter(fmt.Sprintf("action_type == \"%s\"", actionType))
	}

	// Step3: 执行搜索
	results, err := (*vm.milvusClient).Search(ctx, searchOpt)
	if err != nil {
		log.Printf("[VectorMatcher] Milvus 搜索失败: %v", err)
		return nil, fmt.Errorf("向量搜索失败: %w", err)
	}

	// Step4: 解析结果并进行阈值过滤
	var intentVectorInfos []IntentVectorInfo
	isEnglishQuestion := isAllEnglish(content)

	for _, rs := range results {
		for i := 0; i < rs.ResultCount; i++ {
			score := float32(rs.Scores[i])

			// 获取字段值
			resultContent, _ := rs.GetColumn("content").GetAsString(i)
			resultActionType, _ := rs.GetColumn("action_type").GetAsString(i)
			resultActionMsg, _ := rs.GetColumn("action_msg").GetAsString(i)
			resultType, _ := rs.GetColumn("type").GetAsInt64(i)
			resultTenantId, _ := rs.GetColumn("tenant_id").GetAsInt64(i)

			isEnglishAnswer := isAllEnglish(resultContent)

			// 语言一致性检查 + 阈值过滤
			var threshold float32
			if isEnglishQuestion && isEnglishAnswer {
				threshold = ThresholdEnglish
			} else if !isEnglishQuestion && !isEnglishAnswer {
				threshold = ThresholdChinese
			} else {
				continue // 跨语言不匹配，跳过
			}

			if score <= threshold {
				intentVectorInfos = append(intentVectorInfos, IntentVectorInfo{
					Content:    resultContent,
					ActionType: CommandTypeFromString(resultActionType),
					ActionMsg:  resultActionMsg,
					Type:       int(resultType),
					Score:      score,
					TenantId:   int(resultTenantId),
				})
			}
		}
	}

	// Step5: BadCase 过滤
	if len(intentVectorInfos) > 0 && intentVectorInfos[0].ActionType == CommandNone {
		log.Printf("[VectorMatcher] 首个结果为 badcase (none), 返回空列表")
		return nil, nil
	}

	log.Printf("[VectorMatcher] 搜索完成: query=%q, 命中=%d", content, len(intentVectorInfos))
	return intentVectorInfos, nil
}

// isAllEnglish 判断文本是否全为英文字符（用于语言一致性检查）
func isAllEnglish(text string) bool {
	for _, r := range text {
		if r > unicode.MaxASCII && !unicode.IsSpace(r) && !unicode.IsPunct(r) {
			return false
		}
	}
	return true
}
