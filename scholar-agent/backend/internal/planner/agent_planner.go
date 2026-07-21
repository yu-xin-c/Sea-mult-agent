package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"
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

	intentPayload, _ := json.MarshalIndent(intent, "", "  ")
	userPrompt := prompts.PlannerAgentUserPrompt(string(intentPayload))
	msg, err := p.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.PlannerAgentSystemPrompt},
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
	if bp.Type == "repo_discovery" {
		return buildRepoDiscoveryDescription(rawIntent)
	}
	detail := strings.TrimSpace(bp.Description)
	if detail == "" {
		detail = bp.Name
	}
	return prompts.PlannerNodeDescription(bilingualTaskName(bp.Name), detail, rawIntent)
}

func normalizeAssignedTo(value string) string {
	switch strings.TrimSpace(value) {
	case "librarian_agent", "coder_agent", "sandbox_agent", "data_agent", "research_coding_agent", "general_agent":
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
	bp = applyPlannerNodeDefaults(bp)
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
	case "research_coding_agent":
		if containsAny(context, "dataset_profile", "profile dataset", "inspect dataset", "uploaded dataset") {
			return "dataset_profile"
		}
		if containsAny(context, "benchmark_adapter_preflight", "preflight", "repair adapter", "validate adapter") {
			return "benchmark_adapter_preflight"
		}
		if containsAny(context, "benchmark_adapter_generate", "generate adapter", "benchmark adapter", "adapter code") {
			return "benchmark_adapter_generate"
		}
		if containsAny(context, "benchmark_validate", "validate evidence", "verify benchmark", "benchmark evidence") {
			return "benchmark_validate"
		}
		if containsAny(context, "benchmark_execute", "execute benchmark", "run benchmark", "custom benchmark") {
			return "benchmark_execute"
		}
		if containsAny(context, "fix_and_rerun", "result gap", "debug mismatch", "repair and rerun") {
			return "fix_and_rerun"
		}
		if containsAny(context, "paper_code_execute", "execute baseline", "run paper code", "debug baseline") {
			return "paper_code_execute"
		}
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
		if containsAny(context, "ablation_design", "ablation", "消融", "parameter sensitivity", "seed stability") {
			return "ablation_design"
		}
	case "librarian_agent":
		if containsAny(context, "claim_rubric_extract", "claim rubric", "freeze claims", "gradable criteria") {
			return "claim_rubric_extract"
		}
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
		if containsAny(context, "claim_evidence_build", "claim evidence", "evidence graph", "claim verification") {
			return "claim_evidence_build"
		}
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
	case "framework_research", "framework_recommendation", "generate_code", "resolve_dependencies", "prepare_runtime", "install_dependencies", "execute_code", "paper_code_execute", "paper_parse", "claim_rubric_extract", "repo_discovery", "repo_prepare", "ablation_design", "paper_compare", "claim_evidence_build", "result_visualization", "fix_and_rerun", "verify_result", "render_plot", "general_research", "general_synthesis", "general_process", "dataset_profile", "benchmark_adapter_generate", "benchmark_adapter_preflight", "benchmark_execute", "benchmark_validate":
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
	case "research_coding_agent":
		return "benchmark_adapter_generate"
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
	case "dataset_profile", "benchmark_adapter_generate", "benchmark_adapter_preflight", "benchmark_execute", "benchmark_validate", "paper_code_execute", "fix_and_rerun":
		return "research_coding_agent"
	case "generate_code", "resolve_dependencies", "repo_discovery", "repo_prepare":
		return "coder_agent"
	case "prepare_runtime", "install_dependencies", "execute_code":
		return "sandbox_agent"
	case "framework_research", "paper_parse", "claim_rubric_extract", "general_research":
		return "librarian_agent"
	case "framework_recommendation", "ablation_design", "paper_compare", "claim_evidence_build", "result_visualization", "verify_result", "render_plot", "general_synthesis":
		return "data_agent"
	default:
		return bp.AssignedTo
	}
}

func applyPlannerNodeDefaults(bp plannerNodeBlueprint) plannerNodeBlueprint {
	switch bp.Type {
	case "claim_rubric_extract":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "parsed_paper")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "claim_rubric", "claim_rubric_report")
	case "claim_evidence_build":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "claim_rubric", "parsed_paper", "repo_manifest", "reproduction_mode_report", "dependency_install_report", "run_metrics", "paper_debug_report", "paper_patch_manifest", "comparison_report")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "claim_evidence_graph", "claim_verification_report")
	case "repo_discovery":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "parsed_paper")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "candidate_repositories", "repo_validation_report", "repo_url")
		if strings.TrimSpace(bp.Description) == "" {
			bp.Description = prompts.RepoDiscoveryDescription("请根据归一化意图检索论文对应的公开实现仓库。")
		}
	case "repo_prepare":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "repo_url", "candidate_repositories", "repo_validation_report")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "workspace_path", "code_file_path", "generated_code", "repo_manifest")
	case "paper_code_execute":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "code_file_path", "generated_code", "prepared_runtime", "repo_manifest")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "run_metrics", "paper_debug_report", "paper_patch_manifest")
	case "fix_and_rerun":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "code_file_path", "generated_code", "prepared_runtime", "repo_manifest", "run_metrics", "paper_debug_report", "comparison_report")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "rerun_metrics", "rerun_report", "gap_debug_report", "gap_patch_manifest")
	}
	return bp
}

func ensureArtifacts(current []string, required ...string) []string {
	existing := make(map[string]struct{}, len(current))
	out := append([]string(nil), current...)
	for _, item := range current {
		existing[item] = struct{}{}
	}
	for _, item := range required {
		if _, ok := existing[item]; ok {
			continue
		}
		out = append(out, item)
	}
	return out
}
