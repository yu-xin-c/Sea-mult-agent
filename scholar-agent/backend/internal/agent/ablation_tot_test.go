package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"scholar-agent-backend/internal/models"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

func TestExecuteAblationDesignUsesProgressiveTreeAndBudget(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		call := calls.Add(1)
		content := `{"candidates":[
			{"id":"module","category":"module","title":"Remove module","hypothesis":"The module causes the gain.","change":"Disable one module.","metrics":["primary_metric"],"estimated_minutes":8,"estimated_gpu_minutes":4},
			{"id":"runtime","category":"runtime_cost","title":"Measure cost","hypothesis":"The gain survives cost accounting.","change":"Measure two workloads.","metrics":["latency"],"estimated_minutes":5,"estimated_gpu_minutes":2}
		]}`
		switch call {
		case 2:
			content = `{"evaluations":[
				{"id":"module","information_gain":0.95,"relevance":0.9,"reproducibility":0.8,"risk":0.1,"reason":"Directly tests the paper claim."},
				{"id":"runtime","information_gain":0.5,"relevance":0.6,"reproducibility":0.95,"risk":0.05,"reason":"Useful cost baseline."}
			]}`
		case 3:
			content = `{"candidates":[
				{"id":"module_isolated","parent_id":"module","category":"module","title":"Isolate the module boundary","hypothesis":"Only the named submodule is necessary.","change":"Disable only the named submodule while preserving adjacent preprocessing.","metrics":["primary_metric","latency"],"estimated_minutes":6,"estimated_gpu_minutes":3,"expansion_reason":"Separates the module effect from preprocessing."}
			]}`
		case 4:
			content = `{"evaluations":[
				{"id":"module_isolated","information_gain":1,"relevance":1,"reproducibility":0.95,"risk":0.05,"reason":"Provides the cleanest causal comparison."}
			]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-ablation", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{"index": 0, "message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
		})
	}))
	t.Cleanup(server.Close)
	model, err := openaiModel.NewChatModel(context.Background(), &openaiModel.ChatModelConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("NewChatModel returned error: %v", err)
	}

	task := &models.Task{
		Type:        "ablation_design",
		Description: "Design a small ablation",
		Inputs: map[string]any{
			"ablation_max_experiments":  1,
			"ablation_max_gpu_minutes":  10,
			"ablation_max_wall_minutes": 20,
		},
	}
	agent := &DataAgent{Name: "data_agent", ChatModel: model}
	if err := agent.ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatalf("ExecuteTask returned error: %v", err)
	}
	if calls.Load() != 4 {
		t.Fatalf("model calls=%d, want 4", calls.Load())
	}
	if task.Status != models.StatusCompleted {
		t.Fatalf("status=%s", task.Status)
	}
	artifacts, ok := task.Metadata["artifact_values"].(map[string]string)
	if !ok || artifacts["ablation_plan"] == "" || artifacts["selected_ablation_configs"] == "" {
		t.Fatalf("missing structured artifacts: %#v", task.Metadata)
	}
	var plan models.AblationPlan
	if err := json.Unmarshal([]byte(artifacts["ablation_plan"]), &plan); err != nil {
		t.Fatalf("unmarshal plan: %v", err)
	}
	if plan.ActualDepth != 2 || len(plan.ExpandedParents) != 1 || plan.ExpandedParents[0] != "module" {
		t.Fatalf("tree lineage=%#v depth=%d", plan.ExpandedParents, plan.ActualDepth)
	}
	if len(plan.Selected) != 1 || plan.Selected[0].ID != "module_isolated" || plan.Selected[0].ParentID != "module" || plan.Selected[0].Depth != 2 {
		t.Fatalf("selected=%#v", plan.Selected)
	}
}

func TestSelectAblationCandidatesRespectsBudgetAndDiversity(t *testing.T) {
	budget := models.AblationBudget{MaxExperiments: 2, MaxGPUMinutes: 0, MaxWallMinutes: 30}
	plan := selectAblationCandidates(defaultAblationCandidates(), nil, budget)
	if plan.Strategy != "bounded_tree_of_thoughts" {
		t.Fatalf("strategy=%q", plan.Strategy)
	}
	if len(plan.Selected) != 2 {
		t.Fatalf("selected=%d, want 2", len(plan.Selected))
	}
	wallMinutes := 0
	categories := map[string]struct{}{}
	for _, candidate := range plan.Selected {
		wallMinutes += candidate.EstimatedMinutes
		if candidate.DecisionReason == "" {
			t.Fatalf("selected candidate has no decision reason: %#v", candidate)
		}
		if _, exists := categories[candidate.Category]; exists {
			t.Fatalf("duplicate category selected: %s", candidate.Category)
		}
		categories[candidate.Category] = struct{}{}
	}
	if wallMinutes > budget.MaxWallMinutes {
		t.Fatalf("selected wall minutes=%d, budget=%d", wallMinutes, budget.MaxWallMinutes)
	}
	for _, candidate := range plan.Candidates {
		if candidate.DecisionReason == "" {
			t.Fatalf("candidate has no decision reason: %#v", candidate)
		}
	}
}

func TestParseAblationCandidatesRejectsUnknownCategories(t *testing.T) {
	raw := `{"candidates":[
		{"id":"seed test","category":"random_seed","title":"Seed stability","estimated_minutes":-5,"estimated_gpu_minutes":10},
		{"id":"unsafe","category":"rewrite_everything","title":"Unbounded rewrite","estimated_minutes":999}
	]}`
	candidates := parseAblationCandidates(raw)
	if len(candidates) != 1 {
		t.Fatalf("candidates=%#v", candidates)
	}
	if candidates[0].ID != "seed_test" || candidates[0].Category != "seed_stability" {
		t.Fatalf("candidate=%#v", candidates[0])
	}
	if candidates[0].EstimatedMinutes != 0 || candidates[0].EstimatedGPUMinutes != 0 {
		t.Fatalf("negative estimate was not normalized: %#v", candidates[0])
	}
}

func TestEnsureAblationCategoryCoverageFillsMissingDimensions(t *testing.T) {
	generated := make([]models.AblationCandidate, 0, ablationTreeBranchLimit)
	for index := 0; index < ablationTreeBranchLimit; index++ {
		generated = append(generated, models.AblationCandidate{ID: fmt.Sprintf("module-%d", index), Category: "module"})
	}
	merged := ensureAblationCategoryCoverage(generated, defaultAblationCandidates())
	categories := map[string]struct{}{}
	for _, candidate := range merged {
		categories[candidate.Category] = struct{}{}
	}
	for _, category := range []string{"parameter", "module", "data_scale", "seed_stability", "runtime_cost"} {
		if _, exists := categories[category]; !exists {
			t.Fatalf("missing category %q in %#v", category, merged)
		}
	}
	if len(merged) != ablationTreeRootLimit {
		t.Fatalf("merged candidates=%d, root limit=%d", len(merged), ablationTreeRootLimit)
	}
}

func TestScoreAblationCandidatesPreservesExplicitZeroEvaluation(t *testing.T) {
	candidates := []models.AblationCandidate{{ID: "module", Category: "module", EstimatedMinutes: 5}}
	evaluations := map[string]ablationEvaluation{
		"module": {ID: "module", InformationGain: 0, Relevance: 0, Reproducibility: 0, Risk: 0},
	}
	scored := scoreAblationCandidates(candidates, evaluations, models.AblationBudget{MaxWallMinutes: 10})
	if len(scored) != 1 || scored[0].InformationGain != 0 || scored[0].Relevance != 0 || scored[0].Reproducibility != 0 {
		t.Fatalf("explicit zero evaluation was replaced: %#v", scored)
	}
}

func TestParseAblationChildrenRequiresEligibleSameCategoryParent(t *testing.T) {
	parents := []models.AblationCandidate{{ID: "module", ParentID: "root", Depth: 1, Category: "module"}}
	existing := append([]models.AblationCandidate(nil), parents...)
	raw := `{"candidates":[
		{"id":"valid_child","parent_id":"module","category":"module","title":"Valid","hypothesis":"A","change":"B","metrics":["score"],"estimated_minutes":5,"expansion_reason":"More precise"},
		{"id":"wrong_category","parent_id":"module","category":"parameter","title":"Wrong","hypothesis":"A","change":"B","metrics":["score"],"estimated_minutes":5,"expansion_reason":"Wrong category"},
		{"id":"unknown_parent","parent_id":"missing","category":"module","title":"Unknown","hypothesis":"A","change":"B","metrics":["score"],"estimated_minutes":5,"expansion_reason":"Unknown parent"}
	]}`
	children := parseAblationChildren(raw, parents, existing, 3)
	if len(children) != 1 || children[0].ID != "valid_child" || children[0].Depth != 2 {
		t.Fatalf("children=%#v", children)
	}
}
