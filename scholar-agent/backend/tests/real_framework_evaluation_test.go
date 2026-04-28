package tests

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"scholar-agent-backend/internal/agent"
	"scholar-agent-backend/internal/appconfig"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
	"scholar-agent-backend/internal/sandbox"
)

func TestRealFrameworkEvaluationFlow(t *testing.T) {
	if os.Getenv("REAL_FRAMEWORK_EVAL_TEST") == "" {
		t.Skip("跳过真实框架对比测试。如需运行，请设置 REAL_FRAMEWORK_EVAL_TEST=1")
	}
	if _, err := appconfig.LoadLLMConfig(); err != nil {
		t.Skipf("跳过真实框架对比测试：无法从 config.toml 读取 LLM 配置: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	p := planner.NewPlanner()
	plan, err := p.GeneratePlan("比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子，并给出客观对比", "Framework_Evaluation")
	if err != nil {
		t.Fatalf("GeneratePlan failed: %v", err)
	}

	var librarianTask *models.Task
	var coderTasks []*models.Task
	var dataTask *models.Task
	for _, task := range plan.Tasks {
		switch task.AssignedTo {
		case "librarian_agent":
			librarianTask = task
		case "coder_agent":
			coderTasks = append(coderTasks, task)
		case "data_agent":
			dataTask = task
		}
	}

	if librarianTask == nil || len(coderTasks) != 2 || dataTask == nil {
		t.Fatalf("unexpected plan structure: librarian=%v coder=%d data=%v", librarianTask != nil, len(coderTasks), dataTask != nil)
	}

	librarianAgent := agent.NewLibrarianAgent()
	if err := librarianAgent.ExecuteTask(ctx, librarianTask, nil); err != nil {
		t.Fatalf("librarian task failed: %v", err)
	}
	if strings.TrimSpace(librarianTask.Result) == "" {
		t.Fatal("librarian task returned empty result")
	}

	sb := sandbox.NewSandboxClient(os.Getenv("SANDBOX_URL"))
	coderAgent := agent.NewCoderAgent(sb)
	for _, task := range coderTasks {
		task.Description += "\n\n上游文档分析结果：\n" + librarianTask.Result
		if err := coderAgent.ExecuteTask(ctx, task, nil); err != nil {
			t.Fatalf("coder task %q failed: %v", task.Name, err)
		}
		if strings.TrimSpace(task.Code) == "" {
			t.Fatalf("coder task %q generated empty code", task.Name)
		}
		if strings.TrimSpace(task.Result) == "" {
			t.Fatalf("coder task %q returned empty result", task.Name)
		}
		if strings.Contains(task.Result, "达到最大重试次数") {
			t.Fatalf("coder task %q exhausted retries: %s", task.Name, task.Result)
		}
	}

	dataTask.Description += "\n\n上游执行结果汇总：\n"
	for _, task := range coderTasks {
		dataTask.Description += "\n---\n任务: " + task.Name + "\n代码:\n" + task.Code + "\n\n输出:\n" + task.Result + "\n"
	}

	dataAgent := agent.NewDataAgent()
	if err := dataAgent.ExecuteTask(ctx, dataTask, nil); err != nil {
		t.Fatalf("data task failed: %v", err)
	}
	if strings.TrimSpace(dataTask.Result) == "" {
		t.Fatal("data task returned empty report")
	}
}
