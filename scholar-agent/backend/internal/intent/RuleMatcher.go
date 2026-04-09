package intent

import (
	"log"
	"strings"
	"sync"
)

// RuleMatcher 规则匹配器 - 实现精确匹配和模糊匹配的关键词规则系统
type RuleMatcher struct {
	mu       sync.RWMutex
	exactMap map[string]*IntentKeyword // 精确匹配: question == keyword
	fuzzyMap map[string]*IntentKeyword // 模糊匹配: question.contains(keyword)
}

// NewRuleMatcher 创建规则匹配器，预置科研场景关键词
func NewRuleMatcher() *RuleMatcher {
	rm := &RuleMatcher{
		exactMap: make(map[string]*IntentKeyword),
		fuzzyMap: make(map[string]*IntentKeyword),
	}
	rm.loadDefaultRules()
	return rm
}

// Match 匹配用户问题，先精确后模糊，返回命中的关键词和是否命中
func (rm *RuleMatcher) Match(question string) (*IntentKeyword, bool) {
	if question == "" {
		return nil, false
	}

	rm.mu.RLock()
	defer rm.mu.RUnlock()

	// Step1: 精确匹配
	if kw, ok := rm.exactMap[question]; ok {
		log.Printf("[RuleMatcher] 精确匹配命中: question=%q -> type=%s", question, kw.Type)
		return kw, true
	}

	// Step2: 模糊匹配
	for content, kw := range rm.fuzzyMap {
		if strings.Contains(question, content) {
			log.Printf("[RuleMatcher] 模糊匹配命中: question=%q contains=%q -> type=%s", question, content, kw.Type)
			return kw, true
		}
	}

	return nil, false
}

// Refresh 刷新规则缓存（预留，未来从 DB 加载）
func (rm *RuleMatcher) Refresh(exact map[string]*IntentKeyword, fuzzy map[string]*IntentKeyword) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	rm.exactMap = exact
	rm.fuzzyMap = fuzzy
	log.Printf("[RuleMatcher] 规则刷新完成: exact=%d, fuzzy=%d", len(exact), len(fuzzy))
}

// loadDefaultRules 预置科研场景关键词规则
func (rm *RuleMatcher) loadDefaultRules() {
	// 精确匹配规则
	exactRules := []struct {
		content string
		typ     string
	}{
		{"帮我搜论文", "searchPaper"},
		{"搜索论文", "searchPaper"},
		{"引用格式", "citePaper"},
		{"生成引用", "citePaper"},
		{"写摘要", "writeAbstract"},
		{"写综述", "writeLiteratureReview"},
		{"写引言", "writeIntroduction"},
		{"写方法", "writeMethodology"},
		{"写结论", "writeConclusion"},
		{"数据分析", "dataAnalysis"},
		{"画图", "plotChart"},
		{"生成图表", "plotChart"},
		{"实验设计", "experimentDesign"},
		{"公式推导", "formulaDerive"},
		{"论文复现", "paperReproduction"},
		{"框架评估", "frameworkEvaluation"},
		{"框架对比", "frameworkEvaluation"},
	}

	for i, r := range exactRules {
		rm.exactMap[r.content] = &IntentKeyword{
			ID:       int64(i + 1),
			Name:     GetAction(r.typ).Name,
			Type:     r.typ,
			Content:  r.content,
			Strategy: "exact",
		}
	}

	// 模糊匹配规则
	fuzzyRules := []struct {
		content string
		typ     string
	}{
		{"文献综述", "writeLiteratureReview"},
		{"综述", "writeLiteratureReview"},
		{"实验设计", "experimentDesign"},
		{"统计分析", "dataAnalysis"},
		{"图表", "plotChart"},
		{"可视化", "plotChart"},
		{"引用", "citePaper"},
		{"参考文献", "citePaper"},
		{"摘要", "writeAbstract"},
		{"引言", "writeIntroduction"},
		{"方法论", "writeMethodology"},
		{"结论", "writeConclusion"},
		{"润色", "polishText"},
		{"修改文章", "polishText"},
		{"评审", "peerReview"},
		{"审稿", "peerReview"},
		{"公式推导", "formulaDerive"},
		{"推导", "formulaDerive"},
		{"术语", "termExplain"},
		{"名词解释", "termExplain"},
		{"学科分类", "fieldClassify"},
		{"研究方向", "researchAdvice"},
		{"研究建议", "researchAdvice"},
		{"搜论文", "searchPaper"},
		{"找论文", "searchPaper"},
		{"查论文", "searchPaper"},
		{"论文搜索", "searchPaper"},
		{"总结论文", "summarizePaper"},
		{"论文总结", "summarizePaper"},
		{"论文对比", "comparePapers"},
		{"对比论文", "comparePapers"},
		{"复现", "paperReproduction"},
		{"代码执行", "codeExecution"},
		{"运行代码", "codeExecution"},
		{"执行代码", "codeExecution"},
		{"选型", "frameworkEvaluation"},
		{"评估框架", "frameworkEvaluation"},
		{"对比框架", "frameworkEvaluation"},
	}

	for i, r := range fuzzyRules {
		rm.fuzzyMap[r.content] = &IntentKeyword{
			ID:       int64(1000 + i),
			Name:     GetAction(r.typ).Name,
			Type:     r.typ,
			Content:  r.content,
			Strategy: "fuzzy",
		}
	}

	log.Printf("[RuleMatcher] 默认规则加载完成: exact=%d, fuzzy=%d", len(rm.exactMap), len(rm.fuzzyMap))
}
