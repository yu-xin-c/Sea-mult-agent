package planner

import (
	"context"
	"fmt"
	"log"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Planner generates DAGs based on user intent
type Planner struct {
	agent *plannerAgent
}

func NewPlanner() *Planner {
	return &Planner{
		agent: newPlannerAgent(),
	}
}

// BuildPlan creates the new graph-based plan structure described in the refactor docs.
func (p *Planner) BuildPlan(ctx context.Context, intent models.IntentContext) (*models.PlanGraph, error) {
	_ = ctx

	plan := &models.PlanGraph{
		ID:         uuid.New().String(),
		UserIntent: intent.RawIntent,
		IntentType: intent.IntentType,
		Status:     models.StatusPending,
		Nodes:      []*models.TaskNode{},
		Edges:      []*models.TaskEdge{},
		Artifacts:  map[string]models.Artifact{},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	var nodes []*models.TaskNode
	var err error
	if p.agent != nil {
		nodes, err = p.agent.BuildNodes(ctx, intent)
		if err != nil {
			logPlannerFallback(intent, err)
		} else if validationErr := validateAgentPlannedNodes(intent, nodes); validationErr != nil {
			logPlannerFallback(intent, validationErr)
			nodes = nil
		}
	}
	if len(nodes) == 0 {
		switch intent.IntentType {
		case "Framework_Evaluation":
			nodes = buildFrameworkEvaluationNodesV2(intent)
		case "Paper_Reproduction":
			nodes = buildPaperReproductionNodesV2(intent)
		case "Code_Execution":
			nodes = buildCodeExecutionNodesV2(intent)
		default:
			nodes = buildGeneralNodesV2(intent)
		}
	}

	edges := buildEdgesFromNodes(nodes)
	fillInitialStatuses(nodes)
	plan.Nodes = nodes
	plan.Edges = edges
	fillGraphMeta(plan)

	return plan, validatePlanGraph(plan)
}

// GeneratePlan creates a mock DAG for a given intent (for demonstration)
func (p *Planner) GeneratePlan(intent string, intentType string) (*models.Plan, error) {
	planID := uuid.New().String()

	plan := &models.Plan{
		ID:         planID,
		UserIntent: intent,
		Status:     models.StatusPending,
		Tasks:      make(map[string]*models.Task),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	// Legacy plan shape used by older API/tests. The graph planner above is the
	// production path; this remains small and deterministic for compatibility.
	if intentType == "Framework_Evaluation" {
		frameworks := frameworkLabels(strings.ToLower(intent))
		left := frameworkDisplayName(frameworks, 0, "Framework A")
		right := frameworkDisplayName(frameworks, 1, "Framework B")
		useCase := "RAG 问答"
		if hasAny(strings.ToLower(intent), "agent", "智能体") {
			useCase = "Agent 构建"
		}

		t1 := createMockTaskWithDescription(
			"Retrieve Documentation & Best Practices for Target Frameworks",
			"librarian_agent",
			nil,
			fmt.Sprintf("请调研 %s 与 %s 在 %s 场景下的核心架构、典型代码模式、依赖和适用边界。\n原始需求：%s", left, right, useCase, intent),
		)
		t2 := createMockTaskWithDescription(
			"Design Shared RAG Benchmark Protocol",
			"data_agent",
			[]string{t1.ID},
			fmt.Sprintf("请设计一个对 %s 与 %s 都公平的 %s 性能评测协议，固定输入样例、检索语料、指标口径和 JSON 输出字段。\n原始需求：%s", left, right, useCase, intent),
		)
		t3 := createMockTaskWithDescription("Generate "+left+" RAG Benchmark Code", "coder_agent", []string{t1.ID, t2.ID}, frameworkTaskDescription(left, useCase, intent))
		t4 := createMockTaskWithDescription("Generate "+right+" RAG Benchmark Code", "coder_agent", []string{t1.ID, t2.ID}, frameworkTaskDescription(right, useCase, intent))
		t5 := createMockTaskWithDescription("Run "+left+" Benchmark in Sandbox", "sandbox_agent", []string{t3.ID}, frameworkRunTaskDescription(left, intent))
		t6 := createMockTaskWithDescription("Run "+right+" Benchmark in Sandbox", "sandbox_agent", []string{t4.ID}, frameworkRunTaskDescription(right, intent))
		t7 := createMockTaskWithDescription(
			"Compare RAG Benchmark Results",
			"data_agent",
			[]string{t5.ID, t6.ID},
			fmt.Sprintf("请根据 %s 与 %s 的沙箱执行输出生成客观性能对比报告，覆盖实验配置、延迟、成功率、输出质量、依赖复杂度、适用场景和选型建议。\n原始需求：%s", left, right, intent),
		)

		plan.Tasks[t1.ID] = t1
		plan.Tasks[t2.ID] = t2
		plan.Tasks[t3.ID] = t3
		plan.Tasks[t4.ID] = t4
		plan.Tasks[t5.ID] = t5
		plan.Tasks[t6.ID] = t6
		plan.Tasks[t7.ID] = t7
	} else if intentType == "Paper_Reproduction" {
		t1 := createMockTask("Parse Paper & Extract Algorithm Details", "librarian_agent", nil, intent)
		t2 := createMockTask("Find/Clone Open Source Repository", "coder_agent", []string{t1.ID}, intent)
		t3 := createMockTask("Setup Sandbox Environment (Install Dependencies)", "sandbox_agent", []string{t2.ID}, intent)
		t4 := createMockTask("Execute Baseline Code", "sandbox_agent", []string{t3.ID}, intent)
		t5 := createMockTask("Compare Results with Paper", "data_agent", []string{t4.ID}, intent)
		t6 := createMockTask("Debug/Refine Code if Results Mismatch", "coder_agent", []string{t5.ID}, intent)

		plan.Tasks[t1.ID] = t1
		plan.Tasks[t2.ID] = t2
		plan.Tasks[t3.ID] = t3
		plan.Tasks[t4.ID] = t4
		plan.Tasks[t5.ID] = t5
		plan.Tasks[t6.ID] = t6
	} else if intentType == "Code_Execution" {
		t1 := createMockTask("Generate & Run Code", "coder_agent", nil, intent)
		t2 := createMockTask("Verify Results", "data_agent", []string{t1.ID}, intent)

		plan.Tasks[t1.ID] = t1
		plan.Tasks[t2.ID] = t2
	} else {
		// Default simple plan
		t1 := createMockTask("Process Request", "general_agent", nil, intent)
		plan.Tasks[t1.ID] = t1
	}

	return plan, nil
}

// createMockTask 创建一个包含基础信息的模拟任务
func createMockTask(name, agent string, deps []string, context string) *models.Task {
	if deps == nil {
		deps = []string{}
	}
	displayName := bilingualTaskName(name)
	return &models.Task{
		ID:           uuid.New().String(),
		Name:         displayName,
		Description:  prompts.TaskDescription(displayName, context),
		AssignedTo:   agent,
		Status:       models.StatusPending,
		Dependencies: deps,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

func createMockTaskWithDescription(name, agent string, deps []string, description string) *models.Task {
	if deps == nil {
		deps = []string{}
	}
	displayName := bilingualTaskName(name)
	return &models.Task{
		ID:           uuid.New().String(),
		Name:         displayName,
		Description:  description,
		AssignedTo:   agent,
		Status:       models.StatusPending,
		Dependencies: deps,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

var legacyFrameworkPackageMap = map[string][]string{
	"langchain":   {"langchain"},
	"llamaindex":  {"llama-index"},
	"llama-index": {"llama-index"},
	"haystack":    {"haystack-ai"},
	"autogen":     {"pyautogen"},
	"crewai":      {"crewai"},
	"langgraph":   {"langgraph"},
}

func frameworkTaskDescription(name, useCase, intent string) string {
	packages := legacyFrameworkPackages(name)
	packageList := strings.Join(packages, " ")
	primaryPackage := packages[0]

	return fmt.Sprintf(`请编写一个完整的 Python 脚本，在 Docker 沙箱（python:3.9-bullseye）中演示使用该目标框架实现 "%s" 功能。

目标框架：%s
建议安装包：%s

关键要求：
1. 使用该目标框架实现核心流程，依赖声明应围绕目标框架，不要混入另一个被比较框架
2. 使用 Dummy 数据或本地构造样例，不依赖外部数据集、私有密钥或远程 API
3. 运行结束后打印关键指标（如耗时、输出摘要、是否成功）
4. 将结果以 JSON 格式打印到最后一行，格式：{"framework": "%s", "latency_ms": 数字, "output_preview": "字符串"}
5. 脚本逻辑必须自洽，可解释，不能为通过测试而硬编码结果

例如 %s
原始需求：%s`, useCase, name, packageList, name, primaryPackage, intent)
}

func frameworkRunTaskDescription(name, intent string) string {
	return fmt.Sprintf(`请在沙箱中运行 %s 的 RAG 基准测试脚本，并保留完整 stdout/stderr。

运行要求：
1. 安装脚本声明的最小依赖，不引入另一个被比较框架
2. 运行同一份基准协议输入，避免临时改动测试数据
3. 记录安装是否成功、脚本是否成功、端到端耗时、最后一行 JSON 指标和关键错误
4. 如果运行失败，输出可复现的失败原因，供最终报告公平对比

原始需求：%s`, name, intent)
}

func legacyFrameworkPackages(name string) []string {
	key := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", "-"))
	if packages, ok := legacyFrameworkPackageMap[key]; ok && len(packages) > 0 {
		return packages
	}
	return []string{strings.ToLower(strings.TrimSpace(name))}
}

func newNode(name, taskType, agent string, deps, requiredArtifacts, outputArtifacts []string, parallelizable bool, context string) *models.TaskNode {
	if deps == nil {
		deps = []string{}
	}
	if requiredArtifacts == nil {
		requiredArtifacts = []string{}
	}
	if outputArtifacts == nil {
		outputArtifacts = []string{}
	}

	now := time.Now()
	displayName := bilingualTaskName(name)
	return &models.TaskNode{
		ID:                uuid.New().String(),
		Name:              displayName,
		Type:              taskType,
		Description:       prompts.TaskDescription(displayName, context),
		AssignedTo:        agent,
		Status:            models.StatusPending,
		Dependencies:      deps,
		RequiredArtifacts: requiredArtifacts,
		OutputArtifacts:   outputArtifacts,
		Parallelizable:    parallelizable,
		Priority:          0,
		RetryLimit:        0,
		RunCount:          0,
		Inputs:            map[string]any{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func buildFrameworkEvaluationNodes(context string) []*models.TaskNode {
	normalized := strings.ToLower(context)
	frameworks := frameworkLabels(normalized)
	left := frameworkDisplayName(frameworks, 0, "Framework A")
	right := frameworkDisplayName(frameworks, 1, "Framework B")
	needsBenchmark := hasAny(normalized, "benchmark", "性能", "评测", "latency", "吞吐", "run", "运行", "实验")

	t1 := newNode("Research Candidate Frameworks", "framework_research", "librarian_agent", nil, nil, []string{"framework_research_report"}, true, context)
	if !needsBenchmark {
		t2 := newNode("Analyze "+left+" Fit", "framework_fit_left", "librarian_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{"left_fit_report"}, true, context)
		t3 := newNode("Analyze "+right+" Fit", "framework_fit_right", "librarian_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{"right_fit_report"}, true, context)
		t4 := newNode("Generate Selection Recommendation", "framework_recommendation", "data_agent", []string{t2.ID, t3.ID}, []string{"left_fit_report", "right_fit_report"}, []string{"evaluation_report"}, false, context)
		return []*models.TaskNode{t1, t2, t3, t4}
	}

	t2 := newNode("Prepare "+left+" Environment", "framework_prepare_left", "coder_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{"left_env"}, true, context)
	t3 := newNode("Prepare "+right+" Environment", "framework_prepare_right", "coder_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{"right_env"}, true, context)
	t4 := newNode("Run "+left+" Benchmark", "framework_run_left", "sandbox_agent", []string{t2.ID}, []string{"left_env"}, []string{"left_metrics"}, true, context)
	t5 := newNode("Run "+right+" Benchmark", "framework_run_right", "sandbox_agent", []string{t3.ID}, []string{"right_env"}, []string{"right_metrics"}, true, context)
	t6 := newNode("Generate Benchmark Report", "framework_report", "data_agent", []string{t4.ID, t5.ID}, []string{"left_metrics", "right_metrics"}, []string{"evaluation_report"}, false, context)
	return []*models.TaskNode{t1, t2, t3, t4, t5, t6}
}

func buildPaperReproductionNodes(context string) []*models.TaskNode {
	normalized := strings.ToLower(context)
	needsPlot := hasAny(normalized, "plot", "画图", "图表", "曲线", "可视化")
	needsFix := hasAny(normalized, "debug", "fix", "修复", "排查", "不一致")

	t1 := newNode("Parse Paper & Extract Method", "paper_parse", "librarian_agent", nil, nil, []string{"parsed_paper"}, true, context)
	t2 := newRepoDiscoveryNode([]string{t1.ID}, context, models.IntentContext{})
	t3 := newNode("Prepare Workspace", "repo_prepare", "coder_agent", []string{t2.ID}, []string{"repo_url", "candidate_repositories", "repo_validation_report"}, []string{"workspace_path"}, false, context)
	t4 := newNode("Setup Runtime Environment", "env_setup", "sandbox_agent", []string{t3.ID}, []string{"workspace_path"}, []string{"runtime_env"}, false, context)
	t5 := newNode("Execute Baseline", "baseline_run", "sandbox_agent", []string{t4.ID}, []string{"runtime_env"}, []string{"run_metrics"}, false, context)
	t6 := newNode("Compare With Paper Claims", "paper_compare", "data_agent", []string{t5.ID}, []string{"run_metrics", "parsed_paper"}, []string{"comparison_report"}, false, context)

	nodes := []*models.TaskNode{t1, t2, t3, t4, t5, t6}
	lastID := t6.ID
	lastArtifact := "comparison_report"

	if needsPlot {
		t7 := newNode("Visualize Reproduction Results", "result_visualization", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"result_plot"}, true, context)
		nodes = append(nodes, t7)
		lastID = t7.ID
		lastArtifact = "result_plot"
	}
	if needsFix {
		t8 := newNode("Fix Gaps And Rerun", "fix_and_rerun", "coder_agent", []string{lastID}, []string{lastArtifact}, []string{"rerun_report"}, false, context)
		nodes = append(nodes, t8)
	}
	return nodes
}

func buildCodeExecutionNodes(context string) []*models.TaskNode {
	normalized := strings.ToLower(context)
	needsPlot := hasAny(normalized, "plot", "matplotlib", "画图", "图表", "曲线")
	needsAnalysis := hasAny(normalized, "analyze", "analysis", "分析", "解释", "report", "报告")

	t1 := newNode("Generate Code", "generate_code", "coder_agent", nil, nil, []string{"generated_code"}, true, context)
	t2 := newNode("Prepare Runtime", "prepare_runtime", "sandbox_agent", []string{t1.ID}, []string{"generated_code"}, []string{"runtime_env"}, false, context)
	t3 := newNode("Execute Code", "execute_code", "sandbox_agent", []string{t2.ID}, []string{"generated_code", "runtime_env"}, []string{"execution_result"}, false, context)

	nodes := []*models.TaskNode{t1, t2, t3}
	lastID := t3.ID
	lastArtifact := "execution_result"

	if needsPlot {
		t4 := newNode("Render Output Plot", "render_plot", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"plot_image"}, true, context)
		nodes = append(nodes, t4)
		lastID = t4.ID
		lastArtifact = "plot_image"
	}
	if needsAnalysis || !needsPlot {
		t5 := newNode("Verify And Summarize Result", "verify_result", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"verification_report"}, true, context)
		nodes = append(nodes, t5)
	}
	return nodes
}

func buildGeneralNodes(context string) []*models.TaskNode {
	normalized := strings.ToLower(context)
	if hasAny(normalized, "论文", "paper", "总结", "summary", "综述", "report", "报告") {
		t1 := newNode("Collect Background Context", "general_research", "librarian_agent", nil, nil, []string{"background_context"}, true, context)
		t2 := newNode("Synthesize Response", "general_synthesis", "data_agent", []string{t1.ID}, []string{"background_context"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2}
	}

	t1 := newNode("Process Request", "general_process", "general_agent", nil, nil, []string{"general_response"}, true, context)
	return []*models.TaskNode{t1}
}

func buildEdgesFromNodes(nodes []*models.TaskNode) []*models.TaskEdge {
	edges := []*models.TaskEdge{}
	for _, node := range nodes {
		for _, dep := range node.Dependencies {
			edges = append(edges, &models.TaskEdge{
				ID:   uuid.New().String(),
				From: dep,
				To:   node.ID,
				Type: "control",
			})
		}
	}

	producers := map[string]string{}
	for _, node := range nodes {
		for _, key := range node.OutputArtifacts {
			producers[key] = node.ID
		}
	}

	for _, node := range nodes {
		for _, key := range node.RequiredArtifacts {
			if producerID, ok := producers[key]; ok && producerID != node.ID {
				edges = append(edges, &models.TaskEdge{
					ID:   uuid.New().String(),
					From: producerID,
					To:   node.ID,
					Type: "data",
				})
			}
		}
	}

	return edges
}

func fillInitialStatuses(nodes []*models.TaskNode) {
	for _, node := range nodes {
		if len(node.Dependencies) == 0 && len(node.RequiredArtifacts) == 0 {
			node.Status = models.StatusReady
		} else {
			node.Status = models.StatusPending
		}
	}
}

func fillGraphMeta(plan *models.PlanGraph) {
	meta := models.GraphMeta{
		TotalNodes: len(plan.Nodes),
	}

	for _, node := range plan.Nodes {
		switch node.Status {
		case models.StatusCompleted:
			meta.CompletedNodes++
		case models.StatusFailed:
			meta.FailedNodes++
		case models.StatusBlocked:
			meta.BlockedNodes++
		case models.StatusInProgress:
			meta.InProgressNodes++
		case models.StatusReady:
			meta.ReadyNodes++
		}
	}

	plan.Meta = meta
}

func validatePlanGraph(plan *models.PlanGraph) error {
	if plan == nil {
		return fmt.Errorf("plan graph is nil")
	}

	nodeMap := make(map[string]*models.TaskNode, len(plan.Nodes))
	inDegree := make(map[string]int, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if node == nil {
			return fmt.Errorf("plan graph contains nil node")
		}
		if _, exists := nodeMap[node.ID]; exists {
			return fmt.Errorf("duplicate node id: %s", node.ID)
		}
		nodeMap[node.ID] = node
		inDegree[node.ID] = 0
	}

	artifactProducers := map[string]string{}
	for _, node := range plan.Nodes {
		for _, key := range node.OutputArtifacts {
			artifactProducers[key] = node.ID
		}
	}

	adj := map[string][]string{}
	for _, edge := range plan.Edges {
		if edge == nil {
			return fmt.Errorf("plan graph contains nil edge")
		}
		if _, ok := nodeMap[edge.From]; !ok {
			return fmt.Errorf("edge source not found: %s", edge.From)
		}
		if _, ok := nodeMap[edge.To]; !ok {
			return fmt.Errorf("edge target not found: %s", edge.To)
		}
		if edge.Type == "control" {
			inDegree[edge.To]++
			adj[edge.From] = append(adj[edge.From], edge.To)
		}
	}

	for _, node := range plan.Nodes {
		for _, dep := range node.Dependencies {
			if _, ok := nodeMap[dep]; !ok {
				return fmt.Errorf("dependency not found for node %s: %s", node.ID, dep)
			}
		}
		for _, key := range node.RequiredArtifacts {
			if _, ok := artifactProducers[key]; !ok {
				return fmt.Errorf("required artifact has no producer for node %s: %s", node.ID, key)
			}
		}
	}

	queue := make([]string, 0, len(inDegree))
	for id, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++

		for _, next := range adj[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	if visited != len(plan.Nodes) {
		return fmt.Errorf("cycle detected in control edges")
	}

	return nil
}

func buildFrameworkEvaluationNodesV2(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	frameworks := frameworkNamesFromIntent(intent, normalized)
	needsBenchmark := boolEntity(intent.Entities, "needs_benchmark") || hasAny(normalized,
		"benchmark",
		"\u6027\u80fd",
		"\u8bc4\u6d4b",
		"latency",
		"\u541e\u5410",
		"run",
		"\u8fd0\u884c",
		"\u5b9e\u9a8c",
	)

	t1 := newNode("Research Candidate Frameworks", "framework_research", "librarian_agent", nil, nil, []string{"framework_research_report"}, true, context)
	if !needsBenchmark {
		nodes := []*models.TaskNode{t1}
		dependencies := make([]string, 0, len(frameworks))
		requiredArtifacts := make([]string, 0, len(frameworks))
		for idx, framework := range frameworks {
			artifact := fmt.Sprintf("framework_fit_report_%d", idx+1)
			node := newNode("Analyze "+framework+" Fit", fmt.Sprintf("framework_fit_%d", idx+1), "librarian_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{artifact}, true, context)
			nodes = append(nodes, node)
			dependencies = append(dependencies, node.ID)
			requiredArtifacts = append(requiredArtifacts, artifact)
		}
		report := newNode("Generate Selection Recommendation", "framework_recommendation", "data_agent", dependencies, requiredArtifacts, []string{"evaluation_report"}, false, context)
		nodes = append(nodes, report)
		return nodes
	}

	nodes := []*models.TaskNode{t1}
	runDependencies := make([]string, 0, len(frameworks))
	runArtifacts := make([]string, 0, len(frameworks))
	for idx, framework := range frameworks {
		prefix := frameworkArtifactPrefix(framework, idx)
		codeArtifact := fmt.Sprintf("%s_generated_code", prefix)
		dependencyArtifact := fmt.Sprintf("%s_dependency_spec", prefix)
		runtimeArtifact := fmt.Sprintf("%s_runtime_session", prefix)
		preparedArtifact := fmt.Sprintf("%s_prepared_runtime", prefix)
		metricsArtifact := fmt.Sprintf("framework_metrics_%d", idx+1)
		generate := newNode("Generate "+framework+" Benchmark Code", "generate_code", "coder_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{codeArtifact}, true, context)
		resolve := newNode("Resolve "+framework+" Dependencies", "resolve_dependencies", "coder_agent", []string{generate.ID}, []string{codeArtifact}, []string{dependencyArtifact}, true, context)
		prepare := newNode("Prepare "+framework+" Runtime", "prepare_runtime", "sandbox_agent", []string{resolve.ID}, []string{dependencyArtifact}, []string{runtimeArtifact}, true, context)
		install := newNode("Install "+framework+" Dependencies", "install_dependencies", "sandbox_agent", []string{prepare.ID}, []string{runtimeArtifact, dependencyArtifact}, []string{preparedArtifact, prefix + "_dependency_install_report"}, true, context)
		run := newNode("Run "+framework+" Benchmark", "execute_code", "sandbox_agent", []string{install.ID}, []string{codeArtifact, preparedArtifact}, []string{metricsArtifact}, true, context)
		nodes = append(nodes, generate, resolve, prepare, install, run)
		runDependencies = append(runDependencies, run.ID)
		runArtifacts = append(runArtifacts, metricsArtifact)
	}
	report := newNode("Generate Benchmark Report", "framework_report", "data_agent", runDependencies, runArtifacts, []string{"evaluation_report"}, false, context)
	nodes = append(nodes, report)
	return nodes
}

func buildPaperReproductionNodesV2(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	needsPlot := boolEntity(intent.Entities, "needs_plot") || hasAny(normalized,
		"plot",
		"\u753b\u56fe",
		"\u56fe\u8868",
		"\u66f2\u7ebf",
		"\u53ef\u89c6\u5316",
	)
	needsFix := boolEntity(intent.Entities, "needs_fix") || hasAny(normalized,
		"debug",
		"fix",
		"\u4fee\u590d",
		"\u6392\u67e5",
		"\u4e0d\u4e00\u81f4",
		"\u91cd\u8dd1",
	)
	paperTitle := stringEntity(intent.Entities, "paper_title", "Paper")

	t1 := newNode("Parse "+paperTitle+" & Extract Method", "paper_parse", "librarian_agent", nil, nil, []string{"parsed_paper"}, true, context)
	t2 := newRepoDiscoveryNode([]string{t1.ID}, context, intent)
	t3 := newNode("Prepare Workspace", "repo_prepare", "coder_agent", []string{t2.ID}, []string{"repo_url", "candidate_repositories", "repo_validation_report"}, []string{"workspace_path", "code_file_path", "generated_code", "repo_manifest", "reproduction_mode_report"}, false, context)
	t3.Inputs = buildPaperReproductionInputs(intent)
	t4 := newNode("Resolve "+paperTitle+" Dependencies", "resolve_dependencies", "coder_agent", []string{t3.ID}, []string{"workspace_path", "code_file_path", "generated_code", "repo_manifest"}, []string{"dependency_spec"}, false, context)
	t5 := newNode("Setup Runtime Environment", "prepare_runtime", "sandbox_agent", []string{t4.ID}, []string{"workspace_path", "dependency_spec"}, []string{"runtime_session"}, false, context)
	t6 := newNode("Install "+paperTitle+" Dependencies", "install_dependencies", "sandbox_agent", []string{t5.ID}, []string{"runtime_session", "dependency_spec"}, []string{"prepared_runtime", "dependency_install_report"}, false, context)
	t7 := newNode("Execute Baseline", "execute_code", "sandbox_agent", []string{t6.ID}, []string{"workspace_path", "code_file_path", "generated_code", "prepared_runtime"}, []string{"run_metrics"}, false, context)
	t8 := newNode("Compare With Paper Claims", "paper_compare", "data_agent", []string{t7.ID}, []string{"run_metrics", "parsed_paper", "repo_manifest", "reproduction_mode_report"}, []string{"comparison_report"}, false, context)

	nodes := []*models.TaskNode{t1, t2, t3, t4, t5, t6, t7, t8}
	lastID := t8.ID
	lastArtifact := "comparison_report"

	if needsPlot {
		t7 := newNode("Visualize Reproduction Results", "result_visualization", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"result_plot"}, true, context)
		nodes = append(nodes, t7)
		lastID = t7.ID
		lastArtifact = "result_plot"
	}
	if needsFix {
		t8 := newNode("Fix Gaps And Rerun", "fix_and_rerun", "coder_agent", []string{lastID}, []string{lastArtifact}, []string{"rerun_report"}, false, context)
		nodes = append(nodes, t8)
	}
	return nodes
}

func buildPaperReproductionInputs(intent models.IntentContext) map[string]any {
	raw := strings.ToLower(strings.Join([]string{intent.RawIntent, intent.RewrittenIntent}, " "))
	requestedMode := "auto"
	fullRequested := boolEntity(intent.Entities, "full_reproduction") ||
		boolEntity(intent.Constraints, "full_reproduction") ||
		hasAny(raw, "full reproduction", "full run", "bleu", "wmt14", "完整复现", "全量复现", "完整实验", "全量实验")
	if boolEntity(intent.Entities, "smoke_reproduction") ||
		boolEntity(intent.Constraints, "smoke_reproduction") ||
		hasAny(raw, "smoke", "最小实验", "快速验证") {
		requestedMode = "smoke"
	}
	if fullRequested {
		requestedMode = "full"
	}
	if rawMode := strings.TrimSpace(stringEntity(intent.Constraints, "reproduction_mode", "")); rawMode != "" {
		requestedMode = rawMode
	}
	return map[string]any{
		"requested_reproduction_mode": requestedMode,
		"full_reproduction_requested": fullRequested,
	}
}

func buildCodeExecutionNodesV2(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	needsPlot := boolEntity(intent.Entities, "needs_plot") || hasAny(normalized,
		"plot",
		"matplotlib",
		"\u753b\u56fe",
		"\u56fe\u8868",
		"\u66f2\u7ebf",
	)
	needsAnalysis := boolEntity(intent.Entities, "needs_report") || hasAny(normalized,
		"analyze",
		"analysis",
		"\u5206\u6790",
		"\u89e3\u91ca",
		"report",
		"\u62a5\u544a",
		"\u590d\u6742\u5ea6",
	)

	t1 := newNode("Generate Code", "generate_code", "coder_agent", nil, nil, []string{"generated_code"}, true, context)
	t2 := newNode("Resolve Dependencies", "resolve_dependencies", "coder_agent", []string{t1.ID}, []string{"generated_code"}, []string{"dependency_spec"}, true, context)
	t3 := newNode("Prepare Runtime", "prepare_runtime", "sandbox_agent", []string{t2.ID}, []string{"dependency_spec"}, []string{"runtime_session"}, false, context)
	t4 := newNode("Install Dependencies", "install_dependencies", "sandbox_agent", []string{t3.ID}, []string{"runtime_session", "dependency_spec"}, []string{"prepared_runtime", "dependency_install_report"}, false, context)
	t5 := newNode("Execute Code", "execute_code", "sandbox_agent", []string{t4.ID}, []string{"generated_code", "prepared_runtime"}, []string{"execution_result"}, false, context)

	nodes := []*models.TaskNode{t1, t2, t3, t4, t5}
	lastID := t5.ID
	lastArtifact := "execution_result"

	if needsPlot {
		t6 := newNode("Render Output Plot", "render_plot", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"plot_image"}, true, context)
		nodes = append(nodes, t6)
		lastID = t6.ID
		lastArtifact = "plot_image"
	}
	if needsAnalysis || !needsPlot {
		t7 := newNode("Verify And Summarize Result", "verify_result", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"verification_report"}, true, context)
		nodes = append(nodes, t7)
	}
	return nodes
}

func buildGeneralNodesV2(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	if stringEntity(intent.Entities, "paper_task", "") == "summary" {
		title := stringEntity(intent.Entities, "paper_title", "Paper")
		t1 := newNode("Read "+title+" Context", "paper_context", "librarian_agent", nil, nil, []string{"paper_context"}, true, context)
		t2 := newNode("Summarize Contributions", "paper_contributions", "data_agent", []string{t1.ID}, []string{"paper_context"}, []string{"paper_contributions"}, true, context)
		t3 := newNode("List Limitations And Risks", "paper_limitations", "data_agent", []string{t1.ID}, []string{"paper_context"}, []string{"paper_limitations"}, true, context)
		t4 := newNode("Compose Paper Summary", "paper_summary", "data_agent", []string{t2.ID, t3.ID}, []string{"paper_contributions", "paper_limitations"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2, t3, t4}
	}
	if hasAny(normalized,
		"\u8bba\u6587",
		"paper",
		"\u603b\u7ed3",
		"summary",
		"\u7efc\u8ff0",
		"report",
		"\u62a5\u544a",
		"\u8d21\u732e",
		"\u5c40\u9650",
		"rag",
		"query rewrite",
	) {
		t1 := newNode("Collect Background Context", "general_research", "librarian_agent", nil, nil, []string{"background_context"}, true, context)
		t2 := newNode("Synthesize Response", "general_synthesis", "data_agent", []string{t1.ID}, []string{"background_context"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2}
	}

	t1 := newNode("Process Request", "general_process", "general_agent", nil, nil, []string{"general_response"}, true, context)
	return []*models.TaskNode{t1}
}

func hasAny(s string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(s, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func frameworkLabels(normalized string) []string {
	candidates := []string{"langchain", "llamaindex", "haystack", "autogen", "crewai", "langgraph"}
	labels := make([]string, 0, 2)
	for _, candidate := range candidates {
		if strings.Contains(normalized, candidate) {
			labels = append(labels, strings.ToUpper(candidate[:1])+candidate[1:])
		}
	}
	return labels
}

func frameworkNamesFromIntent(intent models.IntentContext, normalized string) []string {
	if raw, ok := intent.Entities["frameworks"].([]string); ok && len(raw) > 0 {
		names := make([]string, 0, len(raw))
		for _, item := range raw {
			names = append(names, prettifyFrameworkName(item))
		}
		return names
	}
	return fallbackFrameworkNames(frameworkLabels(normalized))
}

func fallbackFrameworkNames(frameworks []string) []string {
	if len(frameworks) == 0 {
		return []string{"Framework A", "Framework B"}
	}
	return frameworks
}

func prettifyFrameworkName(name string) string {
	switch strings.ToLower(name) {
	case "langchain":
		return "LangChain"
	case "llamaindex":
		return "LlamaIndex"
	case "langgraph":
		return "LangGraph"
	default:
		return name
	}
}

func boolEntity(entities map[string]any, key string) bool {
	value, ok := entities[key].(bool)
	return ok && value
}

func stringEntity(entities map[string]any, key string, fallback string) string {
	value, ok := entities[key].(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func frameworkDisplayName(frameworks []string, index int, fallback string) string {
	if index < len(frameworks) {
		return frameworks[index]
	}
	return fallback
}

func frameworkArtifactPrefix(framework string, index int) string {
	normalized := strings.ToLower(strings.TrimSpace(framework))
	replacer := strings.NewReplacer(" ", "_", "-", "_", "/", "_")
	normalized = replacer.Replace(normalized)
	if normalized == "" {
		return fmt.Sprintf("framework_%d", index+1)
	}
	return normalized
}

func newRepoDiscoveryNode(deps []string, context string, intent models.IntentContext) *models.TaskNode {
	node := newNode(
		"Retrieve Paper Repositories",
		"repo_discovery",
		"coder_agent",
		deps,
		[]string{"parsed_paper"},
		[]string{"candidate_repositories", "repo_validation_report", "repo_url"},
		true,
		context,
	)
	// 这里用固定流程覆盖默认描述，确保仓库检索节点不再只是“让 LLM 猜一个链接”。
	node.Description = buildRepoDiscoveryDescription(context)
	node.Inputs = buildRepoDiscoveryInputs(intent)
	return node
}

func buildRepoDiscoveryInputs(intent models.IntentContext) map[string]any {
	inputs := map[string]any{}
	if arxivID := stringEntity(intent.Entities, "paper_arxiv_id", ""); arxivID != "" {
		inputs["paper_arxiv_id"] = arxivID
	}
	if title := stringEntity(intent.Entities, "paper_title", ""); title != "" {
		inputs["paper_title"] = title
	}
	if query := stringEntity(intent.Entities, "paper_search_query", ""); query != "" {
		inputs["paper_search_query"] = query
	}
	if method := stringEntity(intent.Entities, "paper_method_name", ""); method != "" {
		inputs["paper_method_name"] = method
	}
	return inputs
}

func buildRepoDiscoveryDescription(rawIntent string) string {
	return prompts.RepoDiscoveryDescription(rawIntent)
}

func bilingualTaskName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return name
	}
	if strings.Contains(trimmed, " / ") {
		return trimmed
	}

	if zh, ok := exactTaskNameTranslations()[trimmed]; ok {
		return zh + " / " + trimmed
	}

	switch {
	case strings.HasPrefix(trimmed, "Analyze ") && strings.HasSuffix(trimmed, " Fit"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Analyze "), " Fit")
		return fmt.Sprintf("分析 %s 适配性 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Prepare ") && strings.HasSuffix(trimmed, " Environment"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Prepare "), " Environment")
		return fmt.Sprintf("准备 %s 环境 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Prepare ") && strings.HasSuffix(trimmed, " Runtime"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Prepare "), " Runtime")
		return fmt.Sprintf("准备 %s 运行环境 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Generate ") && strings.HasSuffix(trimmed, " Benchmark Code"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Generate "), " Benchmark Code")
		return fmt.Sprintf("生成 %s 基准测试代码 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Resolve ") && strings.HasSuffix(trimmed, " Dependencies"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Resolve "), " Dependencies")
		return fmt.Sprintf("解析 %s 依赖 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Install ") && strings.HasSuffix(trimmed, " Dependencies"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Install "), " Dependencies")
		return fmt.Sprintf("安装 %s 依赖 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Run ") && strings.HasSuffix(trimmed, " Benchmark"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Run "), " Benchmark")
		return fmt.Sprintf("运行 %s 基准测试 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Run ") && strings.HasSuffix(trimmed, " Benchmark in Sandbox"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Run "), " Benchmark in Sandbox")
		return fmt.Sprintf("在沙箱中运行 %s 基准测试 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Parse ") && strings.HasSuffix(trimmed, " & Extract Method"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Parse "), " & Extract Method")
		return fmt.Sprintf("解析 %s 并提取方法 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Read ") && strings.HasSuffix(trimmed, " Context"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Read "), " Context")
		return fmt.Sprintf("阅读 %s 背景 / %s", target, trimmed)
	}

	return trimmed
}

func exactTaskNameTranslations() map[string]string {
	return map[string]string{
		"Research Candidate Frameworks":        "调研候选框架",
		"Generate Selection Recommendation":    "生成选型建议",
		"Generate Benchmark Report":            "生成基准测试报告",
		"Design Shared RAG Benchmark Protocol": "设计共享 RAG 评测协议",
		"Compare RAG Benchmark Results":        "对比 RAG 基准测试结果",
		"Locate Reference Repository":          "定位参考仓库",
		"Prepare Workspace":                    "准备工作区",
		"Setup Runtime Environment":            "搭建运行环境",
		"Execute Baseline":                     "执行基线实验",
		"Compare With Paper Claims":            "对比论文声明结果",
		"Visualize Reproduction Results":       "可视化复现实验结果",
		"Fix Gaps And Rerun":                   "修复问题并重新运行",
		"Generate Code":                        "生成代码",
		"Resolve Dependencies":                 "解析依赖",
		"Prepare Runtime":                      "准备运行环境",
		"Install Dependencies":                 "安装依赖",
		"Execute Code":                         "执行代码",
		"Render Output Plot":                   "渲染输出图表",
		"Verify And Summarize Result":          "校验并总结结果",
		"Collect Background Context":           "收集背景信息",
		"Synthesize Response":                  "综合生成回答",
		"Process Request":                      "处理请求",
		"Retrieve Documentation & Best Practices for Target Frameworks": "获取目标框架文档与最佳实践",
		"Generate Environment Setup & Integration Code for Framework A": "为框架 A 生成环境配置与集成代码",
		"Generate Environment Setup & Integration Code for Framework B": "为框架 B 生成环境配置与集成代码",
		"Execute A/B Tests with User Data in Sandbox":                   "在沙箱中执行 A/B 测试",
		"Analyze Metrics & Generate Evaluation Report":                  "分析指标并生成评估报告",
		"Parse Paper & Extract Algorithm Details":                       "解析论文并提取算法细节",
		"Find/Clone Open Source Repository":                             "查找或克隆开源仓库",
		"Setup Sandbox Environment (Install Dependencies)":              "搭建沙箱环境并安装依赖",
		"Compare Results with Paper":                                    "将结果与论文进行对比",
		"Debug/Refine Code if Results Mismatch":                         "结果不一致时调试并优化代码",
		"Summarize Contributions":                                       "总结核心贡献",
		"List Limitations And Risks":                                    "列出局限与风险",
		"Compose Paper Summary":                                         "整理论文总结",
	}
}

func validateAgentPlannedNodes(intent models.IntentContext, nodes []*models.TaskNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("planner agent returned no executable nodes")
	}
	if err := validateCriticalNodeContracts(intent, nodes); err != nil {
		return err
	}
	if intent.IntentType != "Framework_Evaluation" {
		return nil
	}

	normalized := strings.ToLower(intent.RawIntent)
	needsBenchmark := boolEntity(intent.Entities, "needs_benchmark") || hasAny(normalized,
		"benchmark",
		"\u6027\u80fd",
		"\u8bc4\u6d4b",
		"latency",
		"\u541e\u5410",
		"run",
		"\u8fd0\u884c",
		"\u5b9e\u9a8c",
	)
	if !needsBenchmark {
		return nil
	}

	expectedFrameworks := frameworkNamesFromIntent(intent, normalized)
	executePrefixes := map[string]struct{}{}
	prepareCount := 0

	for _, node := range nodes {
		if node == nil || node.AssignedTo != "sandbox_agent" {
			continue
		}
		switch node.Type {
		case "prepare_runtime":
			prepareCount++
		case "execute_code":
			codePrefix := ""
			runtimePrefix := ""
			for _, artifact := range node.RequiredArtifacts {
				switch {
				case strings.HasSuffix(artifact, "_generated_code"):
					codePrefix = strings.TrimSuffix(artifact, "_generated_code")
				case strings.HasSuffix(artifact, "_prepared_runtime"):
					runtimePrefix = strings.TrimSuffix(artifact, "_prepared_runtime")
				}
			}
			if codePrefix == "" || runtimePrefix == "" || codePrefix != runtimePrefix {
				return fmt.Errorf("framework execute node %q is missing isolated code/runtime artifacts", node.Name)
			}
			executePrefixes[codePrefix] = struct{}{}
		}
	}

	if len(executePrefixes) < len(expectedFrameworks) {
		return fmt.Errorf("framework benchmark plan does not contain one isolated execute branch per framework")
	}
	if prepareCount < len(expectedFrameworks) {
		return fmt.Errorf("framework benchmark plan does not contain one runtime branch per framework")
	}
	return nil
}

func validateCriticalNodeContracts(intent models.IntentContext, nodes []*models.TaskNode) error {
	if intent.IntentType != "Code_Execution" && intent.IntentType != "Framework_Evaluation" && intent.IntentType != "Paper_Reproduction" {
		return nil
	}

	hasGeneratedCode := false
	hasResolveDependencies := false
	hasPrepareRuntime := false
	hasInstallDependencies := false
	hasExecuteCode := false
	hasPaperParse := false
	hasRepoDiscovery := false
	hasRepoPrepare := false

	for _, node := range nodes {
		if node == nil {
			continue
		}

		switch node.Type {
		case "generate_code":
			hasGeneratedCode = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("generate_code node %q must be assigned to coder_agent", node.Name)
			}
		case "paper_parse":
			hasPaperParse = true
			if node.AssignedTo != "librarian_agent" {
				return fmt.Errorf("paper_parse node %q must be assigned to librarian_agent", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "parsed_paper") {
				return fmt.Errorf("paper_parse node %q must output parsed_paper", node.Name)
			}
		case "repo_discovery":
			hasRepoDiscovery = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("repo_discovery node %q must be assigned to coder_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "parsed_paper") {
				return fmt.Errorf("repo_discovery node %q must require parsed_paper", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "candidate_repositories") {
				return fmt.Errorf("repo_discovery node %q must output candidate_repositories", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "repo_validation_report") {
				return fmt.Errorf("repo_discovery node %q must output repo_validation_report", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "repo_url") {
				return fmt.Errorf("repo_discovery node %q must output repo_url", node.Name)
			}
		case "repo_prepare":
			hasRepoPrepare = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("repo_prepare node %q must be assigned to coder_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "repo_url") {
				return fmt.Errorf("repo_prepare node %q must require repo_url", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "generated_code") && !containsArtifact(node.OutputArtifacts, "workspace_path") {
				return fmt.Errorf("repo_prepare node %q must output generated_code or workspace_path", node.Name)
			}
		case "resolve_dependencies":
			hasResolveDependencies = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("resolve_dependencies node %q must be assigned to coder_agent", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "dependency_spec") {
				return fmt.Errorf("resolve_dependencies node %q must output dependency_spec", node.Name)
			}
		case "prepare_runtime":
			hasPrepareRuntime = true
			if node.AssignedTo != "sandbox_agent" {
				return fmt.Errorf("prepare_runtime node %q must be assigned to sandbox_agent", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "runtime_session") && !containsArtifact(node.OutputArtifacts, "runtime_env") {
				return fmt.Errorf("prepare_runtime node %q must output runtime_session or runtime_env", node.Name)
			}
		case "install_dependencies":
			hasInstallDependencies = true
			if node.AssignedTo != "sandbox_agent" {
				return fmt.Errorf("install_dependencies node %q must be assigned to sandbox_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "dependency_spec") {
				return fmt.Errorf("install_dependencies node %q must require dependency_spec", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "prepared_runtime") {
				return fmt.Errorf("install_dependencies node %q must output prepared_runtime", node.Name)
			}
		case "execute_code", "baseline_run":
			hasExecuteCode = true
			if node.AssignedTo != "sandbox_agent" {
				return fmt.Errorf("execute_code node %q must be assigned to sandbox_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "generated_code") && !containsArtifact(node.RequiredArtifacts, "code_file_path") {
				return fmt.Errorf("execute_code node %q must require generated code input", node.Name)
			}
		}
	}

	if intent.IntentType == "Code_Execution" {
		if !hasGeneratedCode || !hasResolveDependencies || !hasPrepareRuntime || !hasInstallDependencies || !hasExecuteCode {
			return fmt.Errorf("code execution plan is missing one or more required canonical nodes")
		}
	}
	if intent.IntentType == "Paper_Reproduction" {
		if !hasPaperParse || !hasRepoDiscovery || !hasRepoPrepare || !hasResolveDependencies || !hasPrepareRuntime || !hasInstallDependencies || !hasExecuteCode {
			return fmt.Errorf("paper reproduction plan is missing one or more required canonical nodes")
		}
	}

	return nil
}

func containsArtifact(values []string, artifact string) bool {
	for _, value := range values {
		if value == artifact || strings.HasSuffix(value, "_"+artifact) {
			return true
		}
	}
	return false
}

func logPlannerFallback(intent models.IntentContext, err error) {
	log.Printf("[PlannerAgent] fallback to template planner intent_type=%s raw_intent=%q err=%v", intent.IntentType, intent.RawIntent, err)
}
