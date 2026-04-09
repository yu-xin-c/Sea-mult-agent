package intent

// ChatContext 对话上下文 - 贯穿整个对话生命周期（科研场景精简版，移除情绪相关字段）
type ChatContext struct {
	Question          string            // 用户原始问题文本
	Answer            string            // 回复内容
	TraceId           string            // 链路追踪ID
	SessionId         string            // 会话ID
	UserId            int64             // 用户ID
	DeviceId          string            // 设备ID
	ProductId         string            // 产品ID
	IntentType        string            // 当前意图模式（从缓存读取）
	ToolName          string            // 命中的工具名称
	HitIntent         bool              // 是否命中意图
	ChatType          string            // 对话类型
	IntentVectorInfos []IntentVectorInfo // 向量匹配结果
	GraphInfo         string            // 用户知识图谱
	Knowledge         string            // 知识库
	HistorySummarize  string            // 历史摘要（长期记忆）
	LastHistoryData   string            // 上一轮对话数据
	IsChat            bool              // 是否走闲聊
	Extra             map[string]string // 扩展字段
}

// IntentInfo 意图识别结果
type IntentInfo struct {
	ToolName string // 工具/意图名称（如 "searchPaper", "writeLiteratureReview", "none"）
	Keyword  string // 附带关键词1
	Keyword2 string // 附带关键词2
}

// IntentVectorInfo 向量匹配结果
type IntentVectorInfo struct {
	TenantId   int         // 租户ID
	Content    string      // 匹配内容
	ActionType CommandType // 动作类型枚举
	ActionMsg  string      // 动作消息
	Type       int         // 1=ENTRY进入, 2=EXIT退出
	Score      float32     // 相似度分数（越小越相似）
}

// IntentVectorType 向量匹配文档的进入/退出类型
const (
	VectorTypeEntry = 1 // 进入模式
	VectorTypeExit  = 2 // 退出模式
)

// IntentResult 最终意图识别结果
type IntentResult struct {
	HitIntent   bool              // 是否命中意图
	ToolName    string            // 命中的意图动作名
	Keyword     string            // 附带关键词
	Keyword2    string            // 附带关键词2
	MatchSource MatchSource       // 匹配来源
	Score       float32           // 置信度/相似度分数
	VectorInfos []IntentVectorInfo // 向量匹配结果列表（如有）
	IntentType  string            // 映射到的意图类型（如 Framework_Evaluation）
}

// IntentKeyword 规则匹配关键词数据
type IntentKeyword struct {
	ID       int64  // 唯一标识
	Name     string // 名称
	Type     string // 意图类型: searchPaper, writeLiteratureReview, dataAnalysis...
	Keyword  string // 附带关键词数据
	Content  string // 匹配内容（用于匹配的文本）
	Strategy string // 匹配策略: "exact" 或 "fuzzy"
}

// EngineConfig 意图识别引擎配置
type EngineConfig struct {
	MilvusURL       string // Milvus 地址
	EmbeddingAPIKey string // Embedding API Key
	EmbeddingModel  string // Embedding 模型名称
	EmbeddingURL    string // Embedding API Base URL
	LLMAPIKey       string // LLM API Key
	LLMBaseURL      string // LLM API Base URL
	LLMModel        string // LLM 模型名称
}
