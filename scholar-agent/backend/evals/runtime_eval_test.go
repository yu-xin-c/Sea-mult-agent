package evals

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"scholar-agent-backend/internal/api"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/planner"
)

type runtimeEvalCase struct {
	Name               string   `json:"name"`
	Intent             string   `json:"intent"`
	ExpectedIntentType string   `json:"expected_intent_type"`
	MinNodes           int      `json:"min_nodes"`
	RequiredTaskTypes  []string `json:"required_task_types"`
}

func TestRuntimePlanningEvalDataset(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	data, err := os.ReadFile("runtime_eval_dataset.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []runtimeEvalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}

	passed := 0
	for _, evalCase := range cases {
		t.Run(evalCase.Name, func(t *testing.T) {
			intentType := api.DetectIntentType(evalCase.Intent)
			if intentType != evalCase.ExpectedIntentType {
				t.Fatalf("intent type=%s want=%s", intentType, evalCase.ExpectedIntentType)
			}
			plan, err := planner.NewPlanner().BuildPlan(context.Background(), models.IntentContext{
				RawIntent:   evalCase.Intent,
				IntentType:  intentType,
				Entities:    map[string]any{},
				Constraints: map[string]any{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Nodes) < evalCase.MinNodes {
				t.Fatalf("nodes=%d want>=%d", len(plan.Nodes), evalCase.MinNodes)
			}
			if plan.TraceID == "" || plan.Budget.MaxTaskAttempts <= 0 || plan.Budget.MaxDurationSec <= 0 {
				t.Fatalf("missing runtime governance metadata: %#v", plan)
			}
			taskTypes := map[string]bool{}
			for _, node := range plan.Nodes {
				taskTypes[node.Type] = true
				if node.Contract.Version != models.TaskContractVersion {
					t.Fatalf("task %s contract version=%q", node.ID, node.Contract.Version)
				}
			}
			for _, required := range evalCase.RequiredTaskTypes {
				if !taskTypes[required] {
					t.Fatalf("required task type %q missing from %#v", required, taskTypes)
				}
			}
			passed++
		})
	}
	t.Logf("runtime planning eval: %d/%d cases passed", passed, len(cases))
}
