package tests

import (
	"regexp"
	"strings"
	"testing"

	"scholar-agent-backend/internal/planner"
)

func TestGeneratePlan_FrameworkEvaluationExample(t *testing.T) {
	p := planner.NewPlanner()
	intent := "比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子，并给出客观对比"

	plan, err := p.GeneratePlan(intent, "Framework_Evaluation")
	if err != nil {
		t.Fatalf("GeneratePlan returned error: %v", err)
	}

	if len(plan.Tasks) != 7 {
		t.Fatalf("expected 7 tasks, got %d", len(plan.Tasks))
	}

	var librarianCount, coderCount, sandboxCount, dataCount int
	var coderTasks []string
	var reportTaskFound bool
	var runTaskFound bool

	for _, task := range plan.Tasks {
		switch task.AssignedTo {
		case "librarian_agent":
			librarianCount++
		case "coder_agent":
			coderCount++
			coderTasks = append(coderTasks, task.Description)
			if len(task.Dependencies) != 2 {
				t.Fatalf("expected generated code task %q to depend on research and benchmark protocol, got %d deps", task.Name, len(task.Dependencies))
			}
		case "sandbox_agent":
			sandboxCount++
			if strings.Contains(task.Name, "Benchmark") && len(task.Dependencies) == 1 {
				runTaskFound = true
			}
		case "data_agent":
			dataCount++
			if len(task.Dependencies) == 2 {
				reportTaskFound = true
			}
		}
	}

	if librarianCount != 1 || coderCount != 2 || sandboxCount != 2 || dataCount != 2 {
		t.Fatalf("unexpected task distribution: librarian=%d coder=%d sandbox=%d data=%d", librarianCount, coderCount, sandboxCount, dataCount)
	}
	if !reportTaskFound {
		t.Fatal("expected a data_agent report task depending on both sandbox run tasks")
	}
	if !runTaskFound {
		t.Fatal("expected sandbox benchmark run tasks depending on generated code tasks")
	}

	if len(coderTasks) != 2 {
		t.Fatalf("expected 2 coder tasks, got %d", len(coderTasks))
	}

	normalizedA := normalizeFrameworkDescription(t, coderTasks[0])
	normalizedB := normalizeFrameworkDescription(t, coderTasks[1])
	if normalizedA != normalizedB {
		t.Fatalf("expected symmetric coder task descriptions\nA:\n%s\n\nB:\n%s", normalizedA, normalizedB)
	}

	for _, desc := range coderTasks {
		assertContainsAll(t, desc,
			"使用该目标框架实现",
			"Dummy 数据或本地构造样例",
			"不能为通过测试而硬编码结果",
			"JSON 格式打印到最后一行",
		)
		assertContainsNone(t, desc,
			"优先选择另一个框架",
			"为了通过测试",
			"跳过安装",
		)
	}
}

func normalizeFrameworkDescription(t *testing.T, desc string) string {
	t.Helper()
	replaced := regexp.MustCompile(`目标框架：.*`).ReplaceAllString(desc, "目标框架：<FRAMEWORK>")
	replaced = regexp.MustCompile(`建议安装包：.*`).ReplaceAllString(replaced, "建议安装包：<PACKAGES>")
	replaced = regexp.MustCompile(`例如 .*`).ReplaceAllString(replaced, "例如 <PRIMARY_PACKAGE>")
	replaced = regexp.MustCompile(`\{"framework": ".*?", "latency_ms": 数字, "output_preview": "字符串"\}`).ReplaceAllString(replaced, `{"framework": "<FRAMEWORK>", "latency_ms": 数字, "output_preview": "字符串"}`)
	replaced = regexp.MustCompile(`(?s)\n\[LLM 补充说明\].*`).ReplaceAllString(replaced, "")
	return replaced
}

func assertContainsAll(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if !strings.Contains(text, part) {
			t.Fatalf("expected %q to contain %q", text, part)
		}
	}
}

func assertContainsNone(t *testing.T, text string, parts ...string) {
	t.Helper()
	for _, part := range parts {
		if strings.Contains(text, part) {
			t.Fatalf("expected %q not to contain %q", text, part)
		}
	}
}
