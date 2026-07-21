package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"scholar-agent-backend/internal/agent"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/scheduler"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

type claimEvidenceExampleScenario struct {
	PaperTitle              string          `json:"paper_title"`
	ParsedPaper             string          `json:"parsed_paper"`
	RepoManifest            string          `json:"repo_manifest"`
	ReproductionModeReport  string          `json:"reproduction_mode_report"`
	DependencyInstallReport string          `json:"dependency_install_report"`
	RunMetrics              string          `json:"run_metrics"`
	PaperDebugReport        string          `json:"paper_debug_report"`
	PaperPatchManifest      string          `json:"paper_patch_manifest"`
	ComparisonReport        string          `json:"comparison_report"`
	RubricResponse          json.RawMessage `json:"rubric_response"`
	EvidenceResponse        json.RawMessage `json:"evidence_response"`
	Expected                struct {
		Status                    string  `json:"status"`
		VerifiedClaims            int     `json:"verified_claims"`
		TotalClaims               int     `json:"total_claims"`
		TotalCriteria             int     `json:"total_criteria"`
		CriterionEvidenceCoverage float64 `json:"criterion_evidence_coverage"`
	} `json:"expected"`
}

func TestClaimEvidenceExample(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "test", "claim-evidence")
	scenarioPayload, err := os.ReadFile(filepath.Join(fixtureDir, "scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	var scenario claimEvidenceExampleScenario
	if err := json.Unmarshal(scenarioPayload, &scenario); err != nil {
		t.Fatal(err)
	}

	model := newClaimEvidenceExampleModel(t, scenario.RubricResponse, scenario.EvidenceResponse)
	executor := scheduler.NewRoutedTaskExecutor(
		&agent.LibrarianAgent{Name: "librarian_agent", ChatModel: model},
		&agent.DataAgent{Name: "data_agent", ChatModel: model},
		nil,
	)
	now := time.Date(2026, time.July, 21, 0, 0, 0, 0, time.UTC)
	plan := &models.PlanGraph{
		ID:         "claim-evidence-example",
		UserIntent: "Reproduce the Tiny Reproduction Study and verify each paper claim.",
		IntentType: "Paper_Reproduction",
		Artifacts: map[string]models.Artifact{
			"parsed_paper":              exampleArtifact("parsed_paper", "report", scenario.ParsedPaper, now),
			"repo_manifest":             exampleArtifact("repo_manifest", "json", scenario.RepoManifest, now),
			"reproduction_mode_report":  exampleArtifact("reproduction_mode_report", "report", scenario.ReproductionModeReport, now),
			"dependency_install_report": exampleArtifact("dependency_install_report", "report", scenario.DependencyInstallReport, now),
			"run_metrics":               exampleArtifact("run_metrics", "metrics", scenario.RunMetrics, now),
			"paper_debug_report":        exampleArtifact("paper_debug_report", "report", scenario.PaperDebugReport, now),
			"paper_patch_manifest":      exampleArtifact("paper_patch_manifest", "json", scenario.PaperPatchManifest, now),
			"comparison_report":         exampleArtifact("comparison_report", "report", scenario.ComparisonReport, now),
		},
	}

	rubricNode := &models.TaskNode{
		ID:                "freeze-rubric",
		Name:              "Freeze Hierarchical Claim Rubric",
		Type:              "claim_rubric_extract",
		Description:       "Freeze independently gradable claims before inspecting execution output.",
		AssignedTo:        "librarian_agent",
		RequiredArtifacts: []string{"parsed_paper"},
		OutputArtifacts:   []string{"claim_rubric", "claim_rubric_report"},
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	rubricResult, err := executor.ExecuteTask(context.Background(), plan, rubricNode)
	if err != nil {
		t.Fatal(err)
	}
	if rubricResult.Status != models.StatusCompleted || rubricResult.StructuredData == "" {
		t.Fatalf("rubric task failed: %#v", rubricResult)
	}
	addExampleArtifacts(plan, rubricResult.Artifacts)
	if artifact := plan.Artifacts["claim_rubric"]; artifact.Type != "json" || artifact.Value != rubricResult.StructuredData {
		t.Fatalf("rubric artifact contract mismatch: %#v", artifact)
	}

	evidenceNode := &models.TaskNode{
		ID:          "build-evidence-graph",
		Name:        "Build Claim-to-Evidence Graph",
		Type:        "claim_evidence_build",
		Description: "Bind the frozen rubric to execution-derived evidence.",
		AssignedTo:  "data_agent",
		RequiredArtifacts: []string{
			"claim_rubric", "parsed_paper", "repo_manifest", "reproduction_mode_report",
			"dependency_install_report", "run_metrics", "paper_debug_report",
			"paper_patch_manifest", "comparison_report",
		},
		OutputArtifacts: []string{"claim_evidence_graph", "claim_verification_report"},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	evidenceResult, err := executor.ExecuteTask(context.Background(), plan, evidenceNode)
	if err != nil {
		t.Fatal(err)
	}
	if evidenceResult.Status != models.StatusCompleted || evidenceResult.StructuredData == "" {
		t.Fatalf("evidence task failed: %#v", evidenceResult)
	}
	addExampleArtifacts(plan, evidenceResult.Artifacts)
	graphArtifact := plan.Artifacts["claim_evidence_graph"]
	if graphArtifact.Type != "json" || graphArtifact.Value != evidenceResult.StructuredData {
		t.Fatalf("graph artifact contract mismatch: %#v", graphArtifact)
	}

	var graph models.ClaimEvidenceGraph
	if err := json.Unmarshal([]byte(evidenceResult.StructuredData), &graph); err != nil {
		t.Fatal(err)
	}
	if graph.Status != scenario.Expected.Status ||
		graph.Summary.Verified != scenario.Expected.VerifiedClaims ||
		graph.Summary.TotalClaims != scenario.Expected.TotalClaims ||
		graph.Summary.TotalCriteria != scenario.Expected.TotalCriteria ||
		graph.Summary.CriterionEvidenceCoverage != scenario.Expected.CriterionEvidenceCoverage {
		t.Fatalf("unexpected graph summary: %#v", graph.Summary)
	}
	if len(graph.Claims) != 1 || graph.Claims[0].Status != models.ClaimStatusVerified {
		t.Fatalf("paper claim was not verified: %#v", graph.Claims)
	}
	if got := graph.Claims[0].Criteria[0].EvidenceIDs; !reflect.DeepEqual(got, []string{"evidence-run-metrics", "evidence-comparison-report"}) {
		t.Fatalf("criterion evidence links=%v", got)
	}

	goldenPath := filepath.Join(fixtureDir, "expected_graph.json")
	formattedGraph := formatExampleJSON(t, []byte(evidenceResult.StructuredData))
	if os.Getenv("UPDATE_CLAIM_EVIDENCE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, formattedGraph, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	expectedPayload, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(formatExampleJSON(t, expectedPayload), formattedGraph) {
		t.Fatalf("claim evidence graph differs from golden fixture; run UPDATE_CLAIM_EVIDENCE_GOLDEN=1 go test ./tests -run TestClaimEvidenceExample")
	}

	t.Logf("verified claim=%s criterion=%s evidence=%v", graph.Claims[0].ClaimID, graph.Claims[0].Criteria[0].CriterionID, graph.Claims[0].Criteria[0].EvidenceIDs)
}

func newClaimEvidenceExampleModel(t *testing.T, responses ...json.RawMessage) *openaiModel.ChatModel {
	t.Helper()
	var callCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		index := int(callCount.Add(1)) - 1
		if index >= len(responses) {
			http.Error(w, "unexpected model call", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, string(responses[index]))
	}))
	t.Cleanup(server.Close)

	model, err := openaiModel.NewChatModel(context.Background(), &openaiModel.ChatModelConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "claim-evidence-example",
	})
	if err != nil {
		t.Fatal(err)
	}
	return model
}

func exampleArtifact(key, artifactType, value string, createdAt time.Time) models.Artifact {
	return models.Artifact{Key: key, Type: artifactType, ProducerTaskID: "fixture", Value: value, CreatedAt: createdAt}
}

func addExampleArtifacts(plan *models.PlanGraph, artifacts []models.Artifact) {
	for _, artifact := range artifacts {
		plan.Artifacts[artifact.Key] = artifact
	}
}

func formatExampleJSON(t *testing.T, payload []byte) []byte {
	t.Helper()
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	formatted, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(formatted, '\n')
}
