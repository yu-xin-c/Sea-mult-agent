package intent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
)

// NewMilvusClient 初始化 Milvus 向量数据库客户端（5秒超时，连不上直接报错不阻塞）
func NewMilvusClient(ctx context.Context, url string) (*milvusclient.Client, error) {
	connCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	client, err := milvusclient.New(connCtx, &milvusclient.ClientConfig{
		Address: url,
	})
	if err != nil {
		return nil, fmt.Errorf("连接 Milvus 失败: %w", err)
	}
	return client, nil
}

// EnsureCollection 确保 Milvus 中存在意图向量集合，不存在则创建
func EnsureCollection(ctx context.Context, client *milvusclient.Client) error {
	// 检查集合是否存在
	has, err := (*client).HasCollection(ctx, milvusclient.NewHasCollectionOption(CollectionName))
	if err != nil {
		return fmt.Errorf("检查集合失败: %w", err)
	}
	if has {
		if err = ensureVectorIndex(ctx, client); err != nil {
			return fmt.Errorf("确保向量索引失败: %w", err)
		}
		log.Printf("[MilvusClient] 集合 %s 已存在，尝试加载到内存", CollectionName)
		loadTask, err := (*client).LoadCollection(ctx, milvusclient.NewLoadCollectionOption(CollectionName))
		if err != nil {
			log.Printf("[MilvusClient] 加载集合失败（可能已加载）: %v", err)
		} else {
			// 等待加载完成
			err = loadTask.Await(ctx)
			if err != nil {
				log.Printf("[MilvusClient] 等待集合加载完成失败: %v", err)
			} else {
				log.Printf("[MilvusClient] 集合 %s 已加载到内存", CollectionName)
			}
		}
		return nil
	}

	// 创建集合 Schema
	schema := entity.NewSchema().
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeInt64).WithIsPrimaryKey(true).WithIsAutoID(true)).
		WithField(entity.NewField().WithName("content").WithDataType(entity.FieldTypeVarChar).WithMaxLength(1024)).
		WithField(entity.NewField().WithName("action_type").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64)).
		WithField(entity.NewField().WithName("action_msg").WithDataType(entity.FieldTypeVarChar).WithMaxLength(512)).
		WithField(entity.NewField().WithName("type").WithDataType(entity.FieldTypeInt64)).
		WithField(entity.NewField().WithName("tenant_id").WithDataType(entity.FieldTypeInt64)).
		WithField(entity.NewField().WithName("vector").WithDataType(entity.FieldTypeFloatVector).WithDim(VectorDim))

	createOpt := milvusclient.NewCreateCollectionOption(CollectionName, schema)

	err = (*client).CreateCollection(ctx, createOpt)
	if err != nil {
		return fmt.Errorf("创建集合失败: %w", err)
	}
	if err = ensureVectorIndex(ctx, client); err != nil {
		return fmt.Errorf("创建向量索引失败: %w", err)
	}

	log.Printf("[MilvusClient] 集合 %s 创建成功，尝试加载到内存", CollectionName)

	// 新建集合后也需要 Load
	loadTask, err := (*client).LoadCollection(ctx, milvusclient.NewLoadCollectionOption(CollectionName))
	if err != nil {
		log.Printf("[MilvusClient] 加载新集合失败: %v", err)
	} else {
		if err = loadTask.Await(ctx); err != nil {
			log.Printf("[MilvusClient] 等待新集合加载完成失败: %v", err)
		} else {
			log.Printf("[MilvusClient] 集合 %s 已加载到内存", CollectionName)
		}
	}

	return nil
}

func ensureVectorIndex(ctx context.Context, client *milvusclient.Client) error {
	indexes, err := (*client).ListIndexes(ctx, milvusclient.NewListIndexOption(CollectionName))
	if err != nil {
		if !isIndexNotFoundError(err) {
			return err
		}
		indexes = nil
	}
	if len(indexes) > 0 {
		log.Printf("[MilvusClient] 集合 %s 已存在索引: %v", CollectionName, indexes)
		return nil
	}

	createIndexTask, err := (*client).CreateIndex(ctx, milvusclient.NewCreateIndexOption(
		CollectionName,
		"vector",
		index.NewFlatIndex(entity.COSINE),
	))
	if err != nil {
		return err
	}
	if err = createIndexTask.Await(ctx); err != nil {
		return err
	}
	log.Printf("[MilvusClient] 集合 %s 向量索引创建完成", CollectionName)
	return nil
}

func isIndexNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "index not found")
}

// seedDataEntry 种子数据条目
type seedDataEntry struct {
	Content    string
	ActionType string
	ActionMsg  string
	Type       int64 // 1=ENTRY, 2=EXIT
	TenantId   int64
}

// GetSeedData 获取科研场景预置向量库训练数据
func GetSeedData() []seedDataEntry {
	return []seedDataEntry{
		// 论文搜索变体
		{Content: "找一下关于深度学习的最新论文", ActionType: "paper_reading", ActionMsg: "searchPaper", Type: 1, TenantId: 1},
		{Content: "有没有transformer方面的文章", ActionType: "paper_reading", ActionMsg: "searchPaper", Type: 1, TenantId: 1},
		{Content: "帮我检索一下图神经网络的论文", ActionType: "paper_reading", ActionMsg: "searchPaper", Type: 1, TenantId: 1},
		{Content: "搜索关于大语言模型的研究", ActionType: "paper_reading", ActionMsg: "searchPaper", Type: 1, TenantId: 1},
		{Content: "查找强化学习领域的最新进展", ActionType: "paper_reading", ActionMsg: "searchPaper", Type: 1, TenantId: 1},
		{Content: "search papers about attention mechanism", ActionType: "paper_reading", ActionMsg: "searchPaper", Type: 1, TenantId: 1},

		// 论文总结变体
		{Content: "帮我总结一下这篇论文的核心内容", ActionType: "paper_reading", ActionMsg: "summarizePaper", Type: 1, TenantId: 1},
		{Content: "这篇文章的主要贡献是什么", ActionType: "paper_reading", ActionMsg: "summarizePaper", Type: 1, TenantId: 1},
		{Content: "概括一下这个研究的创新点", ActionType: "paper_reading", ActionMsg: "summarizePaper", Type: 1, TenantId: 1},

		// 写作辅助变体
		{Content: "帮我改一下这段引言", ActionType: "writing_assist", ActionMsg: "writeIntroduction", Type: 1, TenantId: 1},
		{Content: "这个实验部分怎么写比较好", ActionType: "writing_assist", ActionMsg: "writeMethodology", Type: 1, TenantId: 1},
		{Content: "帮我润色一下这段文字", ActionType: "writing_assist", ActionMsg: "polishText", Type: 1, TenantId: 1},
		{Content: "修改一下论文的摘要部分", ActionType: "writing_assist", ActionMsg: "writeAbstract", Type: 1, TenantId: 1},
		{Content: "帮我写一段文献综述", ActionType: "literature_review", ActionMsg: "writeLiteratureReview", Type: 1, TenantId: 1},

		// 数据分析变体
		{Content: "跑一下t检验", ActionType: "data_analysis", ActionMsg: "dataAnalysis", Type: 1, TenantId: 1},
		{Content: "这组数据用什么统计方法好", ActionType: "data_analysis", ActionMsg: "dataAnalysis", Type: 1, TenantId: 1},
		{Content: "帮我做一下回归分析", ActionType: "data_analysis", ActionMsg: "dataAnalysis", Type: 1, TenantId: 1},
		{Content: "画一个数据对比的柱状图", ActionType: "data_analysis", ActionMsg: "plotChart", Type: 1, TenantId: 1},
		{Content: "生成实验结果的可视化图表", ActionType: "data_analysis", ActionMsg: "plotChart", Type: 1, TenantId: 1},

		// 学术术语变体
		{Content: "什么是注意力机制", ActionType: "", ActionMsg: "termExplain", Type: 1, TenantId: 1},
		{Content: "Transformer是什么原理", ActionType: "", ActionMsg: "termExplain", Type: 1, TenantId: 1},
		{Content: "解释一下反向传播算法", ActionType: "", ActionMsg: "termExplain", Type: 1, TenantId: 1},

		// 公式推导
		{Content: "帮我推导一下交叉熵损失函数", ActionType: "formula_derive", ActionMsg: "formulaDerive", Type: 1, TenantId: 1},
		{Content: "推导softmax的梯度公式", ActionType: "formula_derive", ActionMsg: "formulaDerive", Type: 1, TenantId: 1},

		// 退出模式变体
		{Content: "退出论文阅读", ActionType: "paper_reading", ActionMsg: "", Type: 2, TenantId: 1},
		{Content: "不看了", ActionType: "paper_reading", ActionMsg: "", Type: 2, TenantId: 1},
		{Content: "退出写作模式", ActionType: "writing_assist", ActionMsg: "", Type: 2, TenantId: 1},
		{Content: "退出数据分析", ActionType: "data_analysis", ActionMsg: "", Type: 2, TenantId: 1},

		// BadCase (none) - 用于过滤误匹配
		{Content: "你好", ActionType: "", ActionMsg: "none", Type: 1, TenantId: 1},
		{Content: "今天天气怎么样", ActionType: "", ActionMsg: "none", Type: 1, TenantId: 1},
		{Content: "讲个笑话", ActionType: "", ActionMsg: "none", Type: 1, TenantId: 1},
	}
}
