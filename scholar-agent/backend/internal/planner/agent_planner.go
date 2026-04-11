package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"scholar-agent-backend/internal/models"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

type plannerAgent struct {
	enabled   bool
	chatModel *openai.ChatModel
}

type plannerAgentResponse struct {
	Strategy string                 `json:"strategy"`
	Nodes    []plannerNodeBlueprint `json:"nodes"`
}

type plannerNodeBlueprint struct {
	Ref               string   `json:"ref"`
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	AssignedTo        string   `json:"assigned_to"`
	Description       string   `json:"description"`
	Dependencies      []string `json:"dependencies"`
	RequiredArtifacts []string `json:"required_artifacts"`
	OutputArtifacts   []string `json:"output_artifacts"`
	Parallelizable    bool     `json:"parallelizable"`
	Priority          int      `json:"priority"`
}

func newPlannerAgent() *plannerAgent {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		return &plannerAgent{enabled: false}
	}

	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	modelName := os.Getenv("OPENAI_MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	chatModel, err := openai.NewChatModel(context.Background(), &openai.ChatModelConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   modelName,
	})
	if err != nil {
		log.Printf("[PlannerAgent] init failed, fallback to templates: %v", err)
		return &plannerAgent{enabled: false}
	}

	return &plannerAgent{
		enabled:   true,
		chatModel: chatModel,
	}
}

func (p *plannerAgent) BuildNodes(ctx context.Context, intent models.IntentContext) ([]*models.TaskNode, error) {
	if p == nil || !p.enabled || p.chatModel == nil {
		return nil, fmt.Errorf("planner agent is disabled")
	}

	systemPrompt := `You are the planner agent for a multi-agent research backend.
Your job is to output a valid task DAG in strict JSON.

Rules:
1. Output JSON only. No markdown, no comments.
2. Allowed assigned_to values: librarian_agent, coder_agent, sandbox_agent, data_agent, general_agent.
3. Allowed task type values are the canonical runtime types below. Do not invent new task types:
   framework_research, framework_recommendation,
   generate_code, resolve_dependencies, prepare_runtime, install_dependencies, execute_code,
   paper_parse, repo_discovery, repo_prepare, paper_compare, result_visualization, fix_and_rerun,
   verify_result, render_plot, general_research, general_synthesis, general_process.
3. Each node must include:
   ref, name, type, assigned_to, description, dependencies, required_artifacts, output_artifacts, parallelizable, priority.
4. dependencies must reference prior node refs, never IDs.
5. required_artifacts must be produced by prior nodes.
6. For code execution or experiment tasks, prefer explicit environment steps:
   generate_code -> resolve_dependencies -> prepare_runtime -> install_dependencies -> execute_code.
7. For framework comparison, independent framework branches should be runnable in parallel and should join only at the reporting node.
8. For framework comparison, each framework branch must own its own generated_code, dependency_spec, runtime_session, prepared_runtime, and metrics artifacts.
8. For paper reproduction, include environment preparation and execution as separate steps.
9. Keep the DAG minimal but executable.
10. If plotting or reporting is requested, include dedicated downstream nodes for them.

Return JSON with shape:
{
  "strategy": "short explanation",
  "nodes": [
    {
      "ref": "step_key",
      "name": "Human readable name",
      "type": "task_type",
      "assigned_to": "coder_agent",
      "description": "What this node should do",
      "dependencies": ["previous_ref"],
      "required_artifacts": ["artifact_key"],
      "output_artifacts": ["artifact_key"],
      "parallelizable": true,
      "priority": 0
    }
  ]
}`

	intentPayload, _ := json.MarshalIndent(intent, "", "  ")
	userPrompt := fmt.Sprintf("Build an executable DAG for this normalized intent:\n%s", string(intentPayload))
	msg, err := p.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: systemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return nil, err
	}

	parsed, err := parsePlannerAgentResponse(msg.Content)
	if err != nil {
		return nil, err
	}
	return materializePlannerNodes(parsed.Nodes, intent)
}

func parsePlannerAgentResponse(raw string) (*plannerAgentResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var response plannerAgentResponse
	if err := json.Unmarshal([]byte(cleaned), &response); err != nil {
		return nil, fmt.Errorf("parse planner agent response failed: %w", err)
	}
	if len(response.Nodes) == 0 {
		return nil, fmt.Errorf("planner agent returned empty node list")
	}
	return &response, nil
}

func materializePlannerNodes(blueprints []plannerNodeBlueprint, intent models.IntentContext) ([]*models.TaskNode, error) {
	refToID := make(map[string]string, len(blueprints))
	nodes := make([]*models.TaskNode, 0, len(blueprints))

	for _, raw := range blueprints {
		bp := normalizePlannerBlueprint(raw)
		ref := strings.TrimSpace(bp.Ref)
		if ref == "" {
			return nil, fmt.Errorf("planner node ref is empty")
		}
		if _, exists := refToID[ref]; exists {
			return nil, fmt.Errorf("duplicate planner node ref: %s", ref)
		}

		node := newNode(
			chooseString(bp.Name, ref),
			chooseString(bp.Type, "general_process"),
			normalizeAssignedTo(bp.AssignedTo),
			nil,
			cleanStringSlice(bp.RequiredArtifacts),
			cleanStringSlice(bp.OutputArtifacts),
			bp.Parallelizable,
			intent.RawIntent,
		)
		node.Description = buildPlannerNodeDescription(intent.RawIntent, bp)
		node.Priority = bp.Priority
		refToID[ref] = node.ID
		nodes = append(nodes, node)
	}

	for idx, raw := range blueprints {
		bp := normalizePlannerBlueprint(raw)
		deps := make([]string, 0, len(bp.Dependencies))
		for _, depRef := range cleanStringSlice(bp.Dependencies) {
			depID, ok := refToID[depRef]
			if !ok {
				return nil, fmt.Errorf("unknown dependency ref %s for node %s", depRef, bp.Ref)
			}
			deps = append(deps, depID)
		}
		nodes[idx].Dependencies = deps
	}

	return nodes, nil
}

func buildPlannerNodeDescription(rawIntent string, bp plannerNodeBlueprint) string {
	detail := strings.TrimSpace(bp.Description)
	if detail == "" {
		detail = bp.Name
	}
	return fmt.Sprintf("任务目标: %s\n具体要求: %s\n用户原始意图: %s", bp.Name, detail, rawIntent)
}

func normalizeAssignedTo(value string) string {
	switch strings.TrimSpace(value) {
	case "librarian_agent", "coder_agent", "sandbox_agent", "data_agent", "general_agent":
		return value
	default:
		return "general_agent"
	}
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func chooseString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizePlannerBlueprint(bp plannerNodeBlueprint) plannerNodeBlueprint {
	bp.AssignedTo = normalizeAssignedTo(bp.AssignedTo)
	bp.Type = normalizePlannerTaskType(bp)
	bp.AssignedTo = normalizePlannerAssignedTo(bp)
	bp.Dependencies = cleanStringSlice(bp.Dependencies)
	bp.RequiredArtifacts = cleanStringSlice(bp.RequiredArtifacts)
	bp.OutputArtifacts = cleanStringSlice(bp.OutputArtifacts)
	return bp
}

func normalizePlannerTaskType(bp plannerNodeBlueprint) string {
	rawType := normalizeToken(bp.Type)
	if rawType == "" {
		rawType = normalizeToken(bp.Name)
	}
	context := strings.Join([]string{
		rawType,
		normalizeToken(bp.Name),
		normalizeToken(bp.Description),
		strings.Join(normalizeTokens(bp.RequiredArtifacts), " "),
		strings.Join(normalizeTokens(bp.OutputArtifacts), " "),
	}, " ")

	switch bp.AssignedTo {
	case "sandbox_agent":
		if containsAny(context, "install_dependencies", "install dependency", "install package", "pip install", "requirements") {
			return "install_dependencies"
		}
		if containsAny(context, "prepare_runtime", "runtime_session", "prepared_runtime", "prepare environment", "setup runtime", "setup environment", "test environment") {
			return "prepare_runtime"
		}
		if containsAny(context, "execute_code", "run benchmark", "benchmark", "baseline_run", "run code", "execute", "test run", "run experiment") {
			return "execute_code"
		}
	case "coder_agent":
		if containsAny(context, "resolve_dependencies", "dependency_spec", "dependency", "imports", "requirements", "package list") {
			return "resolve_dependencies"
		}
		if containsAny(context, "generate_code", "benchmark code", "integration code", "adapter code", "implementation", "script", "generate") {
			return "generate_code"
		}
		if containsAny(context, "repo_discovery", "repository", "repo url", "find repo", "clone repo") {
			return "repo_discovery"
		}
		if containsAny(context, "repo_prepare", "prepare workspace", "workspace", "checkout") {
			return "repo_prepare"
		}
		if containsAny(context, "fix_and_rerun", "debug", "fix", "repair", "rerun") {
			return "fix_and_rerun"
		}
	case "librarian_agent":
		if containsAny(context, "paper_parse", "extract method", "parse paper") {
			return "paper_parse"
		}
		if containsAny(context, "framework_research", "candidate framework", "research framework", "documentation", "best practice") {
			return "framework_research"
		}
		if containsAny(context, "general_research", "collect background", "background context") {
			return "general_research"
		}
	case "data_agent":
		if containsAny(context, "framework_recommendation", "framework_report", "benchmark report", "selection recommendation") {
			return "framework_recommendation"
		}
		if containsAny(context, "paper_compare", "compare with paper", "comparison report") {
			return "paper_compare"
		}
		if containsAny(context, "render_plot", "result_visualization", "plot", "visualize", "chart") {
			return "render_plot"
		}
		if containsAny(context, "verify_result", "verify", "summarize", "analysis", "report", "summary") {
			return "verify_result"
		}
	}

	switch rawType {
	case "framework_research", "framework_recommendation", "generate_code", "resolve_dependencies", "prepare_runtime", "install_dependencies", "execute_code", "paper_parse", "repo_discovery", "repo_prepare", "paper_compare", "result_visualization", "fix_and_rerun", "verify_result", "render_plot", "general_research", "general_synthesis", "general_process":
		return rawType
	}

	switch bp.AssignedTo {
	case "sandbox_agent":
		return "execute_code"
	case "coder_agent":
		return "generate_code"
	case "librarian_agent":
		return "general_research"
	case "data_agent":
		return "verify_result"
	default:
		return "general_process"
	}
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", "(", " ", ")", " ", ",", " ", ".", " ", "\n", " ")
	value = replacer.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func normalizeTokens(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeToken(value)
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func containsAny(s string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(s, normalizeToken(keyword)) {
			return true
		}
	}
	return false
}

func normalizePlannerAssignedTo(bp plannerNodeBlueprint) string {
	switch bp.Type {
	case "generate_code", "resolve_dependencies", "repo_discovery", "repo_prepare", "fix_and_rerun":
		return "coder_agent"
	case "prepare_runtime", "install_dependencies", "execute_code":
		return "sandbox_agent"
	case "framework_research", "paper_parse", "general_research":
		return "librarian_agent"
	case "framework_recommendation", "paper_compare", "result_visualization", "verify_result", "render_plot", "general_synthesis":
		return "data_agent"
	default:
		return bp.AssignedTo
	}
}
