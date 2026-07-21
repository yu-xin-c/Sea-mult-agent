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
	ablationTreeMaxDepth    = 2
	ablationTreeBranchLimit = 8
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
	candidates := defaultAblationCandidates()

	if a != nil && a.ChatModel != nil {
		msg, err := a.ChatModel.Generate(ctx, []*schema.Message{
			{Role: schema.System, Content: prompts.AblationCandidateSystemPrompt},
			{Role: schema.User, Content: prompts.AblationCandidateUserPrompt(contextText, budget)},
		})
		if err == nil {
			if generated := parseAblationCandidates(msg.Content); len(generated) > 0 {
				candidates = ensureAblationCategoryCoverage(generated, candidates)
			}
		} else {
			logToContext(ctx, "[%s] ToT candidate expansion failed, using bounded defaults: %v", a.Name, err)
		}
	}

	evaluations := map[string]ablationEvaluation{}
	if a != nil && a.ChatModel != nil {
		rawCandidates, _ := json.Marshal(candidates)
		msg, err := a.ChatModel.Generate(ctx, []*schema.Message{
			{Role: schema.System, Content: prompts.AblationEvaluationSystemPrompt},
			{Role: schema.User, Content: prompts.AblationEvaluationUserPrompt(contextText, string(rawCandidates), budget)},
		})
		if err == nil {
			evaluations = parseAblationEvaluations(msg.Content)
		} else {
			logToContext(ctx, "[%s] ToT branch evaluation failed, using deterministic scores: %v", a.Name, err)
		}
	}

	plan := selectAblationCandidates(candidates, evaluations, budget)
	payload, err := json.Marshal(plan)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("marshal ablation plan: %v", err)
		return err
	}
	selectedPayload, _ := json.Marshal(plan.Selected)
	report := fmt.Sprintf(
		"bounded ToT explored %d candidate(s), selected %d within %d experiment(s), %d GPU-minute(s), and %d wall-minute(s)",
		len(plan.Candidates), len(plan.Selected), budget.MaxExperiments, budget.MaxGPUMinutes, budget.MaxWallMinutes,
	)

	task.Result = string(payload)
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

func ensureAblationCategoryCoverage(generated, defaults []models.AblationCandidate) []models.AblationCandidate {
	covered := make(map[string]struct{}, len(generated))
	merged := append([]models.AblationCandidate(nil), generated...)
	for _, candidate := range generated {
		covered[candidate.Category] = struct{}{}
	}
	for _, candidate := range defaults {
		if len(merged) >= ablationTreeBranchLimit {
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
		candidate.Category = sanitizeAblationCategory(candidate.Category)
		if candidate.ID == "" || candidate.Category == "" || strings.TrimSpace(candidate.Title) == "" {
			continue
		}
		if _, exists := seen[candidate.ID]; exists {
			continue
		}
		seen[candidate.ID] = struct{}{}
		if candidate.ParentID == "" {
			candidate.ParentID = "root"
		}
		candidate.EstimatedMinutes = clampInt(candidate.EstimatedMinutes, 1, 240)
		candidate.EstimatedGPUMinutes = clampInt(candidate.EstimatedGPUMinutes, 0, candidate.EstimatedMinutes)
		candidate.Metrics = append([]string(nil), candidate.Metrics...)
		out = append(out, candidate)
		if len(out) >= ablationTreeBranchLimit {
			break
		}
	}
	return out
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
	bounded := make([]models.AblationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if evaluation, ok := evaluations[candidate.ID]; ok {
			candidate.InformationGain = evaluation.InformationGain
			candidate.Relevance = evaluation.Relevance
			candidate.Reproducibility = evaluation.Reproducibility
			candidate.Risk = evaluation.Risk
			candidate.EvaluationReason = strings.TrimSpace(evaluation.Reason)
		}
		candidate.InformationGain = defaultUnit(candidate.InformationGain, 0.6)
		candidate.Relevance = defaultUnit(candidate.Relevance, 0.6)
		candidate.Reproducibility = defaultUnit(candidate.Reproducibility, 0.8)
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

	selected := make([]models.AblationCandidate, 0, budget.MaxExperiments)
	pruned := make([]string, 0)
	usedWall := 0
	usedGPU := 0
	usedCategory := map[string]struct{}{}
	for _, candidate := range bounded {
		_, duplicateCategory := usedCategory[candidate.Category]
		fits := len(selected) < budget.MaxExperiments &&
			usedWall+candidate.EstimatedMinutes <= budget.MaxWallMinutes &&
			usedGPU+candidate.EstimatedGPUMinutes <= budget.MaxGPUMinutes &&
			!duplicateCategory
		if !fits {
			pruned = append(pruned, candidate.ID)
			continue
		}
		selected = append(selected, candidate)
		usedWall += candidate.EstimatedMinutes
		usedGPU += candidate.EstimatedGPUMinutes
		usedCategory[candidate.Category] = struct{}{}
	}

	return models.AblationPlan{
		Strategy:        "bounded_tree_of_thoughts",
		MaxDepth:        ablationTreeMaxDepth,
		BranchLimit:     ablationTreeBranchLimit,
		Budget:          budget,
		Candidates:      bounded,
		Selected:        selected,
		PrunedIDs:       pruned,
		SelectionReason: fmt.Sprintf("greedy beam selection kept %d diverse branch(es) within wall=%d/%d and gpu=%d/%d minutes", len(selected), usedWall, budget.MaxWallMinutes, usedGPU, budget.MaxGPUMinutes),
	}
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
