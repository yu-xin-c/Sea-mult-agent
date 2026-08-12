package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

const (
	ablationTreeMaxDepth      = 2
	ablationTreeRootLimit     = 5
	ablationTreeBranchLimit   = 8
	ablationTreeExpansionBeam = 3
)

type ablationCandidateEnvelope struct {
	Candidates []models.AblationCandidate `json:"candidates"`
}

type ablationEvaluation struct {
	ID              string  `json:"id"`
	InformationGain float64 `json:"information_gain"`
	Relevance       float64 `json:"relevance"`
	Reproducibility float64 `json:"reproducibility"`
	Risk            float64 `json:"risk"`
	Reason          string  `json:"reason"`
}

type ablationEvaluationEnvelope struct {
	Evaluations []ablationEvaluation `json:"evaluations"`
}

func (a *DataAgent) executeAblationDesign(ctx context.Context, task *models.Task) error {
	budget := ablationBudgetFromTask(task)
	contextText := fmt.Sprintf("%s\n\nAvailable artifacts and constraints:\n%v", task.Description, task.Inputs)
	defaults := defaultAblationCandidates()
	rootCandidates := prepareAblationRootCandidates(nil, defaults)

	if a != nil && a.ChatModel != nil {
		msg, err := a.ChatModel.Generate(ctx, []*schema.Message{
			{Role: schema.System, Content: prompts.AblationCandidateSystemPrompt},
			{Role: schema.User, Content: prompts.AblationCandidateUserPrompt(contextText, budget)},
		})
		if err == nil {
			if generated := parseAblationCandidates(msg.Content); len(generated) > 0 {
				rootCandidates = prepareAblationRootCandidates(generated, defaults)
			}
		} else {
			logToContext(ctx, "[%s] ToT candidate expansion failed, using bounded defaults: %v", a.Name, err)
		}
	}

	evaluations := evaluateAblationBranches(ctx, a, contextText, rootCandidates, budget, "root")
	scoredRoots := scoreAblationCandidates(rootCandidates, evaluations, budget)
	candidates := append([]models.AblationCandidate(nil), rootCandidates...)
	expandedParents := []string{}
	remainingBranches := ablationTreeBranchLimit - len(candidates)
	if a != nil && a.ChatModel != nil {
		parents := selectAblationExpansionParents(scoredRoots, budget, minInt(remainingBranches, ablationTreeExpansionBeam))
		if len(parents) > 0 && remainingBranches > 0 {
			rawParents, _ := json.Marshal(parents)
			msg, err := a.ChatModel.Generate(ctx, []*schema.Message{
				{Role: schema.System, Content: prompts.AblationExpansionSystemPrompt},
				{Role: schema.User, Content: prompts.AblationExpansionUserPrompt(contextText, string(rawParents), budget, remainingBranches)},
			})
			if err == nil {
				children := parseAblationChildren(msg.Content, parents, candidates, remainingBranches)
				if len(children) > 0 {
					childEvaluations := evaluateAblationBranches(ctx, a, contextText, children, budget, "child")
					mergeAblationEvaluations(evaluations, childEvaluations)
					candidates = append(candidates, children...)
					expandedParents = ablationExpandedParentIDs(children)
				}
			} else {
				logToContext(ctx, "[%s] ToT child expansion failed, keeping scored root branches: %v", a.Name, err)
			}
		}
	}

	plan := selectAblationCandidates(candidates, evaluations, budget)
	plan.ExpandedParents = expandedParents
	payload, err := json.Marshal(plan)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("marshal ablation plan: %v", err)
		return err
	}
	selectedPayload, _ := json.Marshal(plan.Selected)
	report := fmt.Sprintf(
		"bounded ToT explored %d candidate(s) across %d level(s), selected %d within %d experiment(s), %d GPU-minute(s), and %d wall-minute(s)",
		len(plan.Candidates), plan.ActualDepth, len(plan.Selected), budget.MaxExperiments, budget.MaxGPUMinutes, budget.MaxWallMinutes,
	)

	task.Result = string(payload)
	task.StructuredData = string(payload)
	task.Status = models.StatusCompleted
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]string{
		"ablation_plan":             string(payload),
		"selected_ablation_configs": string(selectedPayload),
		"ablation_selection_report": report,
	}
	logToContext(ctx, "[%s] %s", a.Name, report)
	return nil
}

func evaluateAblationBranches(ctx context.Context, agent *DataAgent, contextText string, candidates []models.AblationCandidate, budget models.AblationBudget, stage string) map[string]ablationEvaluation {
	evaluations := map[string]ablationEvaluation{}
	if agent == nil || agent.ChatModel == nil || len(candidates) == 0 {
		return evaluations
	}
	rawCandidates, _ := json.Marshal(candidates)
	msg, err := agent.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.AblationEvaluationSystemPrompt},
		{Role: schema.User, Content: prompts.AblationEvaluationUserPrompt(contextText, string(rawCandidates), budget)},
	})
	if err != nil {
		logToContext(ctx, "[%s] ToT %s branch evaluation failed, using deterministic scores: %v", agent.Name, stage, err)
		return evaluations
	}
	return parseAblationEvaluations(msg.Content)
}

func prepareAblationRootCandidates(generated, defaults []models.AblationCandidate) []models.AblationCandidate {
	merged := defaults
	if len(generated) > 0 {
		merged = ensureAblationCategoryCoverage(generated, defaults)
	}
	defaultByCategory := make(map[string]models.AblationCandidate, len(defaults))
	for _, candidate := range defaults {
		defaultByCategory[candidate.Category] = candidate
	}
	roots := make([]models.AblationCandidate, 0, ablationTreeRootLimit)
	covered := map[string]struct{}{}
	for _, candidate := range merged {
		if _, duplicate := covered[candidate.Category]; duplicate {
			continue
		}
		candidate = fillAblationCandidateDefaults(candidate, defaultByCategory[candidate.Category])
		candidate.ParentID = "root"
		candidate.Depth = 1
		candidate.ExpansionReason = ""
		roots = append(roots, candidate)
		covered[candidate.Category] = struct{}{}
		if len(roots) >= ablationTreeRootLimit {
			break
		}
	}
	return roots
}

func fillAblationCandidateDefaults(candidate, fallback models.AblationCandidate) models.AblationCandidate {
	if candidate.Hypothesis == "" {
		candidate.Hypothesis = fallback.Hypothesis
	}
	if candidate.Change == "" {
		candidate.Change = fallback.Change
	}
	if len(candidate.Metrics) == 0 {
		candidate.Metrics = append([]string(nil), fallback.Metrics...)
	}
	if candidate.EstimatedMinutes <= 0 {
		candidate.EstimatedMinutes = fallback.EstimatedMinutes
	}
	return candidate
}

func selectAblationExpansionParents(scored []models.AblationCandidate, budget models.AblationBudget, maximum int) []models.AblationCandidate {
	if maximum <= 0 {
		return nil
	}
	if budget.MaxExperiments > 0 && maximum > budget.MaxExperiments {
		maximum = budget.MaxExperiments
	}
	parents := make([]models.AblationCandidate, 0, maximum)
	for _, candidate := range scored {
		if candidate.Depth != 1 || candidate.EstimatedMinutes > budget.MaxWallMinutes || candidate.EstimatedGPUMinutes > budget.MaxGPUMinutes {
			continue
		}
		parents = append(parents, candidate)
		if len(parents) >= maximum {
			break
		}
	}
	return parents
}

func parseAblationChildren(raw string, parents, existing []models.AblationCandidate, maximum int) []models.AblationCandidate {
	if maximum <= 0 {
		return nil
	}
	parentByID := make(map[string]models.AblationCandidate, len(parents))
	for _, parent := range parents {
		parentByID[parent.ID] = parent
	}
	seen := make(map[string]struct{}, len(existing))
	for _, candidate := range existing {
		seen[candidate.ID] = struct{}{}
	}

	children := make([]models.AblationCandidate, 0, maximum)
	for _, candidate := range parseAblationCandidates(raw) {
		parent, ok := parentByID[candidate.ParentID]
		if !ok || candidate.Category != parent.Category || candidate.ID == parent.ID {
			continue
		}
		if _, duplicate := seen[candidate.ID]; duplicate {
			continue
		}
		if strings.TrimSpace(candidate.Hypothesis) == "" || strings.TrimSpace(candidate.Change) == "" || len(candidate.Metrics) == 0 || candidate.EstimatedMinutes <= 0 || strings.TrimSpace(candidate.ExpansionReason) == "" {
			continue
		}
		candidate.Depth = parent.Depth + 1
		if candidate.Depth > ablationTreeMaxDepth {
			continue
		}
		seen[candidate.ID] = struct{}{}
		children = append(children, candidate)
		if len(children) >= maximum {
			break
		}
	}
	return children
}

func mergeAblationEvaluations(target, additions map[string]ablationEvaluation) {
	for id, evaluation := range additions {
		target[id] = evaluation
	}
}

func ablationExpandedParentIDs(children []models.AblationCandidate) []string {
	seen := map[string]struct{}{}
	parents := make([]string, 0, len(children))
	for _, child := range children {
		if _, duplicate := seen[child.ParentID]; duplicate {
			continue
		}
		seen[child.ParentID] = struct{}{}
		parents = append(parents, child.ParentID)
	}
	sort.Strings(parents)
	return parents
}

func ensureAblationCategoryCoverage(generated, defaults []models.AblationCandidate) []models.AblationCandidate {
	covered := make(map[string]struct{}, len(generated))
	merged := make([]models.AblationCandidate, 0, ablationTreeRootLimit)
	for _, candidate := range generated {
		if candidate.Category == "" {
			continue
		}
		if _, duplicate := covered[candidate.Category]; duplicate {
			continue
		}
		merged = append(merged, candidate)
		covered[candidate.Category] = struct{}{}
	}
	for _, candidate := range defaults {
		if len(merged) >= ablationTreeRootLimit {
			break
		}
		if _, exists := covered[candidate.Category]; exists {
			continue
		}
		merged = append(merged, candidate)
		covered[candidate.Category] = struct{}{}
	}
	return merged
}

func ablationBudgetFromTask(task *models.Task) models.AblationBudget {
	return models.AblationBudget{
		MaxExperiments: boundedTaskInt(task, "ablation_max_experiments", 3, 1, 6),
		MaxGPUMinutes:  boundedTaskInt(task, "ablation_max_gpu_minutes", 30, 0, 1440),
		MaxWallMinutes: boundedTaskInt(task, "ablation_max_wall_minutes", 60, 5, 1440),
	}
}

func boundedTaskInt(task *models.Task, key string, fallback, minimum, maximum int) int {
	if task == nil || task.Inputs == nil {
		return fallback
	}
	var value int
	if _, err := fmt.Sscan(fmt.Sprint(task.Inputs[key]), &value); err != nil {
		return fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func defaultAblationCandidates() []models.AblationCandidate {
	return []models.AblationCandidate{
		{ID: "parameter", ParentID: "root", Category: "parameter", Title: "Parameter sensitivity", Hypothesis: "A core parameter controls the reported behavior.", Change: "Vary one structural or optimization parameter around the baseline.", Metrics: []string{"primary_metric", "latency"}, EstimatedMinutes: 15, InformationGain: 0.82, Relevance: 0.88, Reproducibility: 0.92, Risk: 0.12},
		{ID: "module", ParentID: "root", Category: "module", Title: "Module removal", Hypothesis: "A named module is necessary for the claimed gain.", Change: "Disable one core module while keeping the remaining configuration fixed.", Metrics: []string{"primary_metric", "output_delta"}, EstimatedMinutes: 20, InformationGain: 0.94, Relevance: 0.95, Reproducibility: 0.84, Risk: 0.18},
		{ID: "data_scale", ParentID: "root", Category: "data_scale", Title: "Data-scale sensitivity", Hypothesis: "The method behaves differently as the evaluated data size changes.", Change: "Run the same configuration on small and medium bounded data slices.", Metrics: []string{"primary_metric", "throughput"}, EstimatedMinutes: 18, InformationGain: 0.76, Relevance: 0.72, Reproducibility: 0.9, Risk: 0.1},
		{ID: "seed_stability", ParentID: "root", Category: "seed_stability", Title: "Random-seed stability", Hypothesis: "The observed result is stable across random initialization.", Change: "Repeat the baseline with three fixed seeds.", Metrics: []string{"primary_metric_mean", "primary_metric_std"}, EstimatedMinutes: 15, InformationGain: 0.7, Relevance: 0.78, Reproducibility: 0.98, Risk: 0.06},
		{ID: "runtime_cost", ParentID: "root", Category: "runtime_cost", Title: "Runtime-cost comparison", Hypothesis: "The method's gain remains useful after accounting for execution cost.", Change: "Measure latency and memory under two bounded workload sizes.", Metrics: []string{"latency", "throughput", "memory"}, EstimatedMinutes: 10, InformationGain: 0.6, Relevance: 0.68, Reproducibility: 0.96, Risk: 0.05},
	}
}

func parseAblationCandidates(raw string) []models.AblationCandidate {
	raw = cleanJSONResponse(raw)
	var envelope ablationCandidateEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]models.AblationCandidate, 0, len(envelope.Candidates))
	for _, candidate := range envelope.Candidates {
		candidate.ID = sanitizeAblationID(candidate.ID)
		candidate.ParentID = sanitizeAblationID(candidate.ParentID)
		candidate.Category = sanitizeAblationCategory(candidate.Category)
		candidate.Title = strings.TrimSpace(candidate.Title)
		candidate.Hypothesis = strings.TrimSpace(candidate.Hypothesis)
		candidate.Change = strings.TrimSpace(candidate.Change)
		candidate.ExpansionReason = strings.TrimSpace(candidate.ExpansionReason)
		if candidate.ID == "" || candidate.Category == "" || candidate.Title == "" {
			continue
		}
		if _, exists := seen[candidate.ID]; exists {
			continue
		}
		seen[candidate.ID] = struct{}{}
		if candidate.ParentID == "" {
			candidate.ParentID = "root"
		}
		if candidate.EstimatedMinutes > 0 {
			candidate.EstimatedMinutes = clampInt(candidate.EstimatedMinutes, 1, 240)
		} else {
			candidate.EstimatedMinutes = 0
		}
		candidate.EstimatedGPUMinutes = clampInt(candidate.EstimatedGPUMinutes, 0, maxInt(0, candidate.EstimatedMinutes))
		candidate.Metrics = cleanAblationMetrics(candidate.Metrics)
		out = append(out, candidate)
		if len(out) >= ablationTreeBranchLimit {
			break
		}
	}
	return out
}

func cleanAblationMetrics(values []string) []string {
	seen := map[string]struct{}{}
	metrics := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		metrics = append(metrics, value)
		if len(metrics) >= 8 {
			break
		}
	}
	return metrics
}

func parseAblationEvaluations(raw string) map[string]ablationEvaluation {
	raw = cleanJSONResponse(raw)
	var envelope ablationEvaluationEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return map[string]ablationEvaluation{}
	}
	out := make(map[string]ablationEvaluation, len(envelope.Evaluations))
	for _, evaluation := range envelope.Evaluations {
		evaluation.ID = sanitizeAblationID(evaluation.ID)
		if evaluation.ID == "" {
			continue
		}
		evaluation.InformationGain = clampUnit(evaluation.InformationGain)
		evaluation.Relevance = clampUnit(evaluation.Relevance)
		evaluation.Reproducibility = clampUnit(evaluation.Reproducibility)
		evaluation.Risk = clampUnit(evaluation.Risk)
		out[evaluation.ID] = evaluation
	}
	return out
}

func selectAblationCandidates(candidates []models.AblationCandidate, evaluations map[string]ablationEvaluation, budget models.AblationBudget) models.AblationPlan {
	bounded := scoreAblationCandidates(candidates, evaluations, budget)

	selected := make([]models.AblationCandidate, 0, budget.MaxExperiments)
	pruned := make([]string, 0)
	usedWall := 0
	usedGPU := 0
	usedCategory := map[string]string{}
	actualDepth := 0
	for index := range bounded {
		candidate := &bounded[index]
		if candidate.Depth > actualDepth {
			actualDepth = candidate.Depth
		}
		reasons := make([]string, 0, 4)
		if len(selected) >= budget.MaxExperiments {
			reasons = append(reasons, "experiment-count budget reached")
		}
		if selectedID, duplicateCategory := usedCategory[candidate.Category]; duplicateCategory {
			reasons = append(reasons, fmt.Sprintf("category %s already represented by %s", candidate.Category, selectedID))
		}
		if usedWall+candidate.EstimatedMinutes > budget.MaxWallMinutes {
			reasons = append(reasons, fmt.Sprintf("wall budget would reach %d/%d minutes", usedWall+candidate.EstimatedMinutes, budget.MaxWallMinutes))
		}
		if usedGPU+candidate.EstimatedGPUMinutes > budget.MaxGPUMinutes {
			reasons = append(reasons, fmt.Sprintf("GPU budget would reach %d/%d minutes", usedGPU+candidate.EstimatedGPUMinutes, budget.MaxGPUMinutes))
		}
		if len(reasons) > 0 {
			candidate.DecisionReason = "pruned: " + strings.Join(reasons, "; ")
			pruned = append(pruned, candidate.ID)
			continue
		}
		candidate.DecisionReason = fmt.Sprintf("selected at score rank %d: category is not yet represented and estimated cost fits the remaining budget", index+1)
		selected = append(selected, *candidate)
		usedWall += candidate.EstimatedMinutes
		usedGPU += candidate.EstimatedGPUMinutes
		usedCategory[candidate.Category] = candidate.ID
	}

	return models.AblationPlan{
		Strategy:        "bounded_tree_of_thoughts",
		MaxDepth:        ablationTreeMaxDepth,
		ActualDepth:     actualDepth,
		BranchLimit:     ablationTreeBranchLimit,
		Budget:          budget,
		Candidates:      bounded,
		Selected:        selected,
		PrunedIDs:       pruned,
		SelectionReason: fmt.Sprintf("greedy beam selection over %d level(s) kept %d diverse branch(es) within wall=%d/%d and gpu=%d/%d minutes", actualDepth, len(selected), usedWall, budget.MaxWallMinutes, usedGPU, budget.MaxGPUMinutes),
	}
}

func scoreAblationCandidates(candidates []models.AblationCandidate, evaluations map[string]ablationEvaluation, budget models.AblationBudget) []models.AblationCandidate {
	bounded := make([]models.AblationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ParentID == "" {
			candidate.ParentID = "root"
		}
		if candidate.Depth <= 0 {
			candidate.Depth = 1
			if candidate.ParentID != "root" {
				candidate.Depth = 2
			}
		}
		if evaluation, ok := evaluations[candidate.ID]; ok {
			candidate.InformationGain = evaluation.InformationGain
			candidate.Relevance = evaluation.Relevance
			candidate.Reproducibility = evaluation.Reproducibility
			candidate.Risk = evaluation.Risk
			candidate.EvaluationReason = strings.TrimSpace(evaluation.Reason)
		} else {
			candidate.InformationGain = defaultUnit(candidate.InformationGain, 0.6)
			candidate.Relevance = defaultUnit(candidate.Relevance, 0.6)
			candidate.Reproducibility = defaultUnit(candidate.Reproducibility, 0.8)
		}
		candidate.Risk = clampUnit(candidate.Risk)
		costPenalty := math.Min(1, float64(candidate.EstimatedMinutes)/float64(maxInt(1, budget.MaxWallMinutes)))
		candidate.Score = roundScore(0.4*candidate.InformationGain + 0.3*candidate.Relevance + 0.2*candidate.Reproducibility - 0.07*costPenalty - 0.03*candidate.Risk)
		bounded = append(bounded, candidate)
		if len(bounded) >= ablationTreeBranchLimit {
			break
		}
	}
	sort.SliceStable(bounded, func(i, j int) bool {
		if bounded[i].Score == bounded[j].Score {
			return bounded[i].EstimatedMinutes < bounded[j].EstimatedMinutes
		}
		return bounded[i].Score > bounded[j].Score
	})
	return bounded
}

func cleanJSONResponse(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	return strings.TrimSpace(raw)
}

func sanitizeAblationID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer(" ", "_", "-", "_", "/", "_").Replace(value)
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			builder.WriteRune(r)
		}
	}
	return strings.Trim(builder.String(), "_")
}

func sanitizeAblationCategory(value string) string {
	value = sanitizeAblationID(value)
	aliases := map[string]string{
		"parameters": "parameter", "hyperparameter": "parameter", "hyperparameters": "parameter",
		"modules": "module", "component": "module", "component_removal": "module",
		"data": "data_scale", "dataset": "data_scale", "data_size": "data_scale",
		"seed": "seed_stability", "random_seed": "seed_stability", "stability": "seed_stability",
		"cost": "runtime_cost", "runtime": "runtime_cost", "efficiency": "runtime_cost",
	}
	if alias, ok := aliases[value]; ok {
		value = alias
	}
	switch value {
	case "parameter", "module", "data_scale", "seed_stability", "runtime_cost":
		return value
	default:
		return ""
	}
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func clampUnit(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func defaultUnit(value, fallback float64) float64 {
	if value <= 0 {
		return fallback
	}
	return clampUnit(value)
}

func roundScore(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
