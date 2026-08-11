package prompts

import (
	"strings"
	"testing"
)

func TestTaskPromptIsolation(t *testing.T) {
	frameworkCoder := CoderSystemPromptForTask("Framework_Evaluation", "generate_code", "Generate LangChain Benchmark Code", "")
	if !strings.Contains(frameworkCoder, "框架对比 / RAG Benchmark") || strings.Contains(frameworkCoder, "论文复现硬性约束") {
		t.Fatalf("framework coder prompt is not isolated")
	}

	paperCoder := CoderSystemPromptForTask("Paper_Reproduction", "execute_code", "Execute Baseline", "")
	if !strings.Contains(paperCoder, "论文复现硬性约束") || strings.Contains(paperCoder, "框架对比 / RAG Benchmark") {
		t.Fatalf("paper coder prompt is not isolated")
	}

	frameworkReport := DataSystemPromptForTask("Framework_Evaluation", "framework_report", "Generate Benchmark Report", "")
	if !strings.Contains(frameworkReport, "框架对比报告规则") || strings.Contains(frameworkReport, "论文复现报告规则") {
		t.Fatalf("framework data prompt is not isolated")
	}

	paperReport := DataSystemPromptForTask("Paper_Reproduction", "paper_compare", "Compare With Paper Claims", "")
	if !strings.Contains(paperReport, "论文复现报告规则") || strings.Contains(paperReport, "框架对比报告规则") {
		t.Fatalf("paper data prompt is not isolated")
	}

	autoResearchReport := DataSystemPromptForTask("Paper_Reproduction", "autoresearch_report", "论文复现 AutoResearch 总结", "")
	if !strings.Contains(autoResearchReport, "AutoResearch 报告规则") || !strings.Contains(autoResearchReport, "不能称独立验证") || strings.Contains(autoResearchReport, "论文复现报告规则") {
		t.Fatalf("AutoResearch data prompt is not isolated")
	}

	frameworkResearch := LibrarianSystemPromptForTask("Framework_Evaluation", "framework_research", "Research Candidate Frameworks", "")
	if !strings.Contains(frameworkResearch, "技术框架调研员") || strings.Contains(frameworkResearch, "论文复现分析员") {
		t.Fatalf("framework librarian prompt is not isolated")
	}

	paperParse := LibrarianSystemPromptForTask("Paper_Reproduction", "paper_parse", "Parse Paper", "")
	if !strings.Contains(paperParse, "论文复现分析员") || strings.Contains(paperParse, "技术框架调研员") {
		t.Fatalf("paper librarian prompt is not isolated")
	}
}
