package agent

import (
	"encoding/json"
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestClaimRubricExtractionFreezesStableContract(t *testing.T) {
	modelResponse := `{
		"paper_title":"Tiny Reproduction Study",
		"claims":[{
			"title":"Accuracy claim",
			"statement":"The method reaches 80 percent accuracy.",
			"source_locator":"Table 1",
			"claim_type":"quantitative",
			"importance":0.9,
			"criteria":[{
				"description":"Evaluate accuracy using the paper protocol.",
				"metric_name":"accuracy",
				"expected_value":0.8,
				"tolerance":0.01,
				"unit":"ratio",
				"required_evidence":["paper","run","metric"]
			}]
		}]
	}`
	task := &models.Task{
		Name:        "Freeze rubric",
		Type:        "claim_rubric_extract",
		Description: "Extract independently gradable paper claims.",
		Inputs:      map[string]any{"parsed_paper": "Tiny Reproduction Study, Table 1: accuracy 80%."},
	}
	agent := &LibrarianAgent{Name: "librarian_agent", ChatModel: newTestChatModel(t, modelResponse)}

	if err := agent.executeClaimRubricExtraction(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	if task.Status != models.StatusCompleted || task.StructuredData == "" {
		t.Fatalf("unexpected task result: status=%s structured=%t", task.Status, task.StructuredData != "")
	}

	var rubric models.ClaimRubric
	if err := json.Unmarshal([]byte(task.StructuredData), &rubric); err != nil {
		t.Fatal(err)
	}
	if rubric.Version != models.ClaimRubricVersion || rubric.SHA256 == "" {
		t.Fatalf("rubric contract was not frozen: %#v", rubric)
	}
	if rubric.Claims[0].ID != "claim-001" || rubric.Claims[0].Criteria[0].ID != "claim-001.criterion-01" {
		t.Fatalf("unstable claim IDs: %#v", rubric.Claims[0])
	}
	if got := claimRubricSHA256(rubric); got != rubric.SHA256 {
		t.Fatalf("rubric hash mismatch: got=%s want=%s", got, rubric.SHA256)
	}
	values, ok := task.Metadata["artifact_values"].(map[string]string)
	if !ok || values["claim_rubric"] == "" || values["claim_rubric_report"] == "" {
		t.Fatalf("claim artifacts were not routed: %#v", task.Metadata)
	}
}

func TestClaimEvidenceBuildRequiresDirectExecutionEvidence(t *testing.T) {
	rubric := testClaimRubric(t)
	rubricJSON, err := json.Marshal(rubric)
	if err != nil {
		t.Fatal(err)
	}
	proposal := `{"findings":[{
		"claim_id":"claim-001",
		"criterion_id":"claim-001.criterion-01",
		"status":"verified",
		"confidence":0.95,
		"observed_value":"repository documents the metric",
		"evidence_keys":["repo_manifest"],
		"reason":"The repository describes an accuracy target."
	}]}`
	task := &models.Task{
		Name: "Build evidence graph",
		Type: "claim_evidence_build",
		Inputs: map[string]any{
			"claim_rubric":  string(rubricJSON),
			"repo_manifest": `{"entrypoint":"train.py"}`,
		},
	}
	agent := &DataAgent{Name: "data_agent", ChatModel: newTestChatModel(t, proposal)}

	if err := agent.executeClaimEvidenceBuild(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	graph := decodeClaimEvidenceGraph(t, task.StructuredData)
	verdict := graph.Claims[0].Criteria[0]
	if verdict.Status != models.ClaimStatusUnverifiable || verdict.Confidence > 0.25 {
		t.Fatalf("non-execution evidence incorrectly verified a claim: %#v", verdict)
	}
	if graph.Claims[0].Title == "" || verdict.Description == "" {
		t.Fatalf("visualization labels are missing: %#v", graph.Claims[0])
	}
}

func TestClaimEvidenceBuildDegradesIncompleteAdjudication(t *testing.T) {
	rubric := testClaimRubric(t)
	rubric.Claims[0].Criteria = append(rubric.Claims[0].Criteria, models.ClaimCriterion{
		ID:               "claim-001.criterion-02",
		Description:      "Run a second independently gradable check.",
		RequiredEvidence: []string{"run"},
	})
	rubric.SHA256 = claimRubricSHA256(rubric)
	rubricJSON, err := json.Marshal(rubric)
	if err != nil {
		t.Fatal(err)
	}
	proposal := `{"findings":[{
		"claim_id":"claim-001",
		"criterion_id":"claim-001.criterion-01",
		"status":"verified",
		"confidence":0.9,
		"evidence_keys":["run_metrics"],
		"reason":"The measured accuracy matches."
	}]}`
	task := &models.Task{
		Type: "claim_evidence_build",
		Inputs: map[string]any{
			"claim_rubric": string(rubricJSON),
			"run_metrics":  `{"accuracy":0.8}`,
		},
	}
	agent := &DataAgent{Name: "data_agent", ChatModel: newTestChatModel(t, proposal)}

	if err := agent.executeClaimEvidenceBuild(t.Context(), task); err != nil {
		t.Fatal(err)
	}
	graph := decodeClaimEvidenceGraph(t, task.StructuredData)
	if graph.Status != "degraded" || graph.Summary.Unverifiable != 1 {
		t.Fatalf("incomplete adjudication was not degraded: %#v", graph)
	}
	for _, verdict := range graph.Claims[0].Criteria {
		if verdict.Status != models.ClaimStatusUnverifiable {
			t.Fatalf("degraded graph retained an unsupported verdict: %#v", verdict)
		}
	}
}

func TestClaimEvidenceRejectsRehashedNonCanonicalRubric(t *testing.T) {
	rubric := testClaimRubric(t)
	rubric.Claims[0].Criteria[0].ID = "criterion-forged"
	rubric.SHA256 = claimRubricSHA256(rubric)
	raw, err := json.Marshal(rubric)
	if err != nil {
		t.Fatal(err)
	}

	_, _, err = claimRubricFromTask(&models.Task{Inputs: map[string]any{"claim_rubric": string(raw)}})
	if err == nil {
		t.Fatal("expected a rehashed non-canonical rubric to be rejected")
	}
}

func testClaimRubric(t *testing.T) models.ClaimRubric {
	t.Helper()
	rubric, err := normalizeClaimRubric(models.ClaimRubric{
		PaperTitle: "Tiny Reproduction Study",
		Claims: []models.PaperClaim{{
			Title:         "Accuracy claim",
			Statement:     "The method reaches 80 percent accuracy.",
			SourceLocator: "Table 1",
			ClaimType:     "quantitative",
			Importance:    0.9,
			Criteria: []models.ClaimCriterion{{
				Description:      "Evaluate accuracy using the paper protocol.",
				MetricName:       "accuracy",
				RequiredEvidence: []string{"paper", "run", "metric"},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rubric
}

func decodeClaimEvidenceGraph(t *testing.T, raw string) models.ClaimEvidenceGraph {
	t.Helper()
	var graph models.ClaimEvidenceGraph
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		t.Fatal(err)
	}
	return graph
}
