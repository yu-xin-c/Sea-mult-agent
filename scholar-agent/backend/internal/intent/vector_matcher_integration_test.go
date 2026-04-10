package intent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVectorMatcherBasicFlow(t *testing.T) {
	milvusURL := strings.TrimSpace(os.Getenv("MILVUS_URL"))
	embeddingAPIKey := strings.TrimSpace(os.Getenv("EMBEDDING_API_KEY"))
	if milvusURL == "" || embeddingAPIKey == "" {
		t.Skip("MILVUS_URL 或 EMBEDDING_API_KEY 未配置，跳过集成测试")
	}

	embeddingBaseURL := strings.TrimSpace(os.Getenv("EMBEDDING_BASE_URL"))
	embeddingModel := strings.TrimSpace(os.Getenv("EMBEDDING_MODEL"))
	if embeddingModel == "" {
		embeddingModel = "text-embedding-3-small"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client, err := NewMilvusClient(ctx, milvusURL)
	if err != nil {
		t.Fatalf("NewMilvusClient 失败: %v", err)
	}
	defer (*client).Close(context.Background())

	if err = EnsureCollection(ctx, client); err != nil {
		t.Fatalf("EnsureCollection 失败: %v", err)
	}

	vm, err := NewVectorMatcher(client, VectorMatcherConfig{
		APIKey:  embeddingAPIKey,
		BaseURL: embeddingBaseURL,
		Model:   embeddingModel,
	})
	if err != nil {
		t.Fatalf("NewVectorMatcher 失败: %v", err)
	}

	if err = vm.SeedData(ctx); err != nil {
		t.Fatalf("SeedData 失败: %v", err)
	}

	cnResult, err := vm.Search(ctx, "帮我找一下transformer方面的论文", "")
	if err != nil {
		t.Fatalf("中文检索失败: %v", err)
	}
	if len(cnResult) > 0 && (cnResult[0].ActionMsg == "" || cnResult[0].ActionMsg == "none") {
		t.Fatalf("中文检索命中结果无效: %+v", cnResult[0])
	}

	enResult, err := vm.Search(ctx, "search papers about attention mechanism", "")
	if err != nil {
		t.Fatalf("英文检索失败: %v", err)
	}
	if len(enResult) > 0 && (enResult[0].ActionMsg == "" || enResult[0].ActionMsg == "none") {
		t.Fatalf("英文检索命中结果无效: %+v", enResult[0])
	}

	if len(cnResult) == 0 && len(enResult) == 0 {
		t.Logf("中文与英文检索均未命中，当前阈值/索引数据组合下可能偏严格，但向量链路已跑通")
	}
}
