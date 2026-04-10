package intent

// MatchSource 匹配来源
type MatchSource string

const (
	MatchSourceRule   MatchSource = "rule"   // 规则匹配
	MatchSourceVector MatchSource = "vector" // 向量匹配
	MatchSourceLLM    MatchSource = "llm"    // LLM推理
	MatchSourceNone   MatchSource = "none"   // 未命中
)

// IntentAction 意图动作定义
type IntentAction struct {
	Action string // 动作标识
	Name   string // 名称
	Desc   string // 描述
}

// ResearchIntentActions 科研场景意图动作枚举
var ResearchIntentActions = map[string]IntentAction{
	// 文献管理意图
	"searchPaper":    {Action: "searchPaper", Name: "论文搜索", Desc: "搜索特定主题/作者/期刊的论文"},
	"citePaper":      {Action: "citePaper", Name: "论文引用", Desc: "引用某篇论文或生成引用格式"},
	"summarizePaper": {Action: "summarizePaper", Name: "论文摘要", Desc: "对论文内容进行总结"},
	"comparePapers":  {Action: "comparePapers", Name: "论文对比", Desc: "对比多篇论文的方法/结论"},

	// 写作辅助意图
	"writeAbstract":         {Action: "writeAbstract", Name: "撰写摘要", Desc: "辅助撰写论文摘要"},
	"writeIntroduction":     {Action: "writeIntroduction", Name: "撰写引言", Desc: "辅助撰写论文引言"},
	"writeLiteratureReview": {Action: "writeLiteratureReview", Name: "文献综述", Desc: "辅助撰写文献综述"},
	"writeMethodology":      {Action: "writeMethodology", Name: "撰写方法", Desc: "辅助撰写研究方法论"},
	"writeConclusion":       {Action: "writeConclusion", Name: "撰写结论", Desc: "辅助撰写论文结论"},
	"polishText":            {Action: "polishText", Name: "文本润色", Desc: "学术写作润色和修改"},

	// 数据分析意图
	"dataAnalysis":     {Action: "dataAnalysis", Name: "数据分析", Desc: "进行实验数据的统计分析"},
	"plotChart":        {Action: "plotChart", Name: "图表生成", Desc: "生成数据可视化图表"},
	"experimentDesign": {Action: "experimentDesign", Name: "实验设计", Desc: "辅助设计实验方案"},

	// 学术交流意图
	"peerReview":     {Action: "peerReview", Name: "同行评审", Desc: "对论文进行审阅评价"},
	"researchAdvice": {Action: "researchAdvice", Name: "研究建议", Desc: "提供研究方向和建议"},
	"fieldClassify":  {Action: "fieldClassify", Name: "学科分类", Desc: "识别和分类学科领域"},
	"termExplain":    {Action: "termExplain", Name: "术语解释", Desc: "解释学术专业术语"},
	"formulaDerive":  {Action: "formulaDerive", Name: "公式推导", Desc: "数学公式推导和验证"},

	// 代码与复现意图
	"codeExecution":      {Action: "codeExecution", Name: "代码执行", Desc: "运行或执行代码"},
	"paperReproduction":  {Action: "paperReproduction", Name: "论文复现", Desc: "复现论文中的实验"},
	"frameworkEvaluation": {Action: "frameworkEvaluation", Name: "框架评估", Desc: "对比评估技术框架"},

	// 通用意图
	"chat": {Action: "chat", Name: "闲聊", Desc: "非学术场景的一般对话"},
	"none": {Action: "none", Name: "无意图", Desc: "未识别到明确意图"},
}

// IsValidAction 检查动作名是否合法
func IsValidAction(action string) bool {
	_, ok := ResearchIntentActions[action]
	return ok
}

// GetAction 获取意图动作，不存在则返回 none
func GetAction(action string) IntentAction {
	if a, ok := ResearchIntentActions[action]; ok {
		return a
	}
	return ResearchIntentActions["none"]
}

// intentTypeMapping 意图动作到系统 intentType 的映射
var intentTypeMapping = map[string]string{
	// Framework_Evaluation
	"comparePapers":       "Framework_Evaluation",
	"frameworkEvaluation": "Framework_Evaluation",

	// Paper_Reproduction
	"paperReproduction": "Paper_Reproduction",

	// Code_Execution
	"codeExecution":    "Code_Execution",
	"dataAnalysis":     "Code_Execution",
	"plotChart":        "Code_Execution",
	"experimentDesign": "Code_Execution",
	"formulaDerive":    "Code_Execution",

	// General (所有写作/文献相关)
	"searchPaper":          "General",
	"citePaper":            "General",
	"summarizePaper":       "General",
	"writeAbstract":        "General",
	"writeIntroduction":    "General",
	"writeLiteratureReview": "General",
	"writeMethodology":     "General",
	"writeConclusion":      "General",
	"polishText":           "General",
	"peerReview":           "General",
	"researchAdvice":       "General",
	"fieldClassify":        "General",
	"termExplain":          "General",
	"chat":                 "General",
	"none":                 "General",
}

// MapToIntentType 将意图动作映射到系统级 intentType
func MapToIntentType(action string) string {
	if t, ok := intentTypeMapping[action]; ok {
		return t
	}
	return "General"
}
