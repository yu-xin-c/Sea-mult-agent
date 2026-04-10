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
	// VectorDim 向量维度（doubao-embedding-text-240715 为 2560 维）
	VectorDim = 2560
	// DefaultTopK 默认检索返回数量
	DefaultTopK = 10
	// ThresholdChinese 中文阈值（COSINE 相似度，越大越相似，>= 此值视为命中）
	ThresholdChinese = float32(0.75)
	// ThresholdEnglish 英文阈值
	ThresholdEnglish = float32(0.70)
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

			log.Printf("[VectorMatcher] 原始结果[%d]: score=%.6f, content=%q, actionMsg=%s", i, score, resultContent, resultActionMsg)

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

			if score >= threshold {
				intentVectorInfos = append(intentVectorInfos, IntentVectorInfo{
					Content:    resultContent,
					ActionType: resultActionType,
					ActionMsg:  resultActionMsg,
					Type:       int(resultType),
					Score:      score,
					TenantId:   int(resultTenantId),
				})
			}
		}
	}

	// Step5: BadCase 过滤
	if len(intentVectorInfos) > 0 && intentVectorInfos[0].ActionType == "" {
		log.Printf("[VectorMatcher] 首个结果为 badcase (none), 返回空列表")
		return nil, nil
	}

	log.Printf("[VectorMatcher] 搜索完成: query=%q, 命中=%d", content, len(intentVectorInfos))

	//  可能需要 加入 Rerank 的 模型() 进行重排序 这样的话 可以提高 检索的 精准度
	return intentVectorInfos, nil
}

// SeedData 检查集合是否为空，若为空则插入预训练种子数据（向量化 + 批量插入）
func (vm *VectorMatcher) SeedData(ctx context.Context) error {
	// 检查集合中是否已有数据
	queryOpt := milvusclient.NewQueryOption(CollectionName)
	queryOpt.WithLimit(1)
	queryOpt.WithOutputFields("content")

	resultSet, err := (*vm.milvusClient).Query(ctx, queryOpt)
	if err == nil && resultSet.GetColumn("content") != nil && resultSet.GetColumn("content").Len() > 0 {
		log.Printf("[VectorMatcher] 集合中已有数据，跳过种子数据插入")
		return nil
	}

	seeds := GetSeedData()
	if len(seeds) == 0 {
		return nil
	}

	log.Printf("[VectorMatcher] 开始插入 %d 条种子数据...", len(seeds))

	// 批量提取 content 文本
	contents := make([]string, len(seeds))
	for i, s := range seeds {
		contents[i] = s.Content
	}

	// 批量向量化
	vectors, err := vm.embedder.EmbedStrings(ctx, contents)
	if err != nil {
		return fmt.Errorf("种子数据向量化失败: %w", err)
	}
	if len(vectors) != len(seeds) {
		return fmt.Errorf("向量化结果数量不匹配: expected=%d, actual=%d", len(seeds), len(vectors))
	}

	// 构建列数据
	contentCol := make([]string, len(seeds))
	actionTypeCol := make([]string, len(seeds))
	actionMsgCol := make([]string, len(seeds))
	typeCol := make([]int64, len(seeds))
	tenantIdCol := make([]int64, len(seeds))
	vectorCol := make([][]float32, len(seeds))

	for i, s := range seeds {
		contentCol[i] = s.Content
		actionTypeCol[i] = s.ActionType
		actionMsgCol[i] = s.ActionMsg
		typeCol[i] = s.Type
		tenantIdCol[i] = s.TenantId
		vectorCol[i] = make([]float32, len(vectors[i]))
		for j, v := range vectors[i] {
			vectorCol[i][j] = float32(v)
		}
	}

	// 插入 Milvus
	insertOpt := milvusclient.NewColumnBasedInsertOption(CollectionName).
		WithVarcharColumn("content", contentCol).
		WithVarcharColumn("action_type", actionTypeCol).
		WithVarcharColumn("action_msg", actionMsgCol).
		WithInt64Column("type", typeCol).
		WithInt64Column("tenant_id", tenantIdCol).
		WithFloatVectorColumn("vector", VectorDim, vectorCol)

	_, err = (*vm.milvusClient).Insert(ctx, insertOpt)
	if err != nil {
		return fmt.Errorf("种子数据插入 Milvus 失败: %w", err)
	}

	// Flush 确保数据持久化
	flushTask, err := (*vm.milvusClient).Flush(ctx, milvusclient.NewFlushOption(CollectionName))
	if err != nil {
		log.Printf("[VectorMatcher] Flush 失败（数据可能延迟可见）: %v", err)
	} else {
		if err = flushTask.Await(ctx); err != nil {
			log.Printf("[VectorMatcher] 等待 Flush 完成失败: %v", err)
		}
	}

	log.Printf("[VectorMatcher] 成功插入 %d 条种子数据到集合 %s", len(seeds), CollectionName)
	return nil
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
