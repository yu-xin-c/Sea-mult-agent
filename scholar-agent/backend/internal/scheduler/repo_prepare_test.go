package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestRepoPrepareCandidateURLs_NormalizesAndAddsFallbacks(t *testing.T) {
	candidates, _ := json.Marshal([]repoCandidate{
		{
			RepoName: "brandokoch/attention-is-all-you-need-paper",
			RepoURLs: []string{
				"https://github.com/brandokoch/attention-is-all-you-need-paper",
			},
		},
		{
			RepoName: "example/transformer",
			RepoURLs: []string{
				"https://github.com/example/transformer.git",
			},
		},
	})
	task := &models.Task{
		Description: "Global user intent: reproduce Attention Is All You Need",
		Inputs: map[string]any{
			"candidate_repositories": string(candidates),
		},
	}

	urls := repoPrepareCandidateURLs(task, "https://github.com/brandokoch/attention-is-all-you-need-paper.git")
	expected := []string{
		"https://github.com/brandokoch/attention-is-all-you-need-paper",
		"https://github.com/harvardnlp/annotated-transformer",
		"https://github.com/example/transformer",
	}
	if len(urls) != len(expected) {
		t.Fatalf("expected %d urls, got %d: %#v", len(expected), len(urls), urls)
	}
	for i := range expected {
		if urls[i] != expected[i] {
			t.Fatalf("url[%d]: expected %q, got %q", i, expected[i], urls[i])
		}
	}
}

func TestMaybeCreateReproductionSmokeRunner_AttentionTransformer(t *testing.T) {
	workspace := t.TempDir()
	modelPath := filepath.Join(workspace, "src", "architectures", "machine_translation_transformer.py")
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("# repo model\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runnerPath, kind, err := maybeCreateReproductionSmokeRunner(workspace, nil)
	if err != nil {
		t.Fatalf("maybeCreateReproductionSmokeRunner returned error: %v", err)
	}
	if kind != "bounded_forward_pass" {
		t.Fatalf("unexpected smoke kind: %q", kind)
	}
	if filepath.Base(runnerPath) != reproductionSmokeRunnerName {
		t.Fatalf("unexpected runner path: %s", runnerPath)
	}
	raw, err := os.ReadFile(runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(raw); !strings.Contains(text, "MachineTranslationTransformer") || !strings.Contains(text, "bounded_forward_pass") {
		t.Fatalf("runner does not contain expected smoke reproduction code:\n%s", text)
	}
}

func TestMaybeCreateReproductionSmokeRunner_GenericAttentionRepo(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("Attention Is All You Need annotated transformer notebook"), 0o644); err != nil {
		t.Fatal(err)
	}

	runnerPath, kind, err := maybeCreateReproductionSmokeRunner(workspace, &models.Task{Name: "Prepare Attention Is All You Need workspace"})
	if err != nil {
		t.Fatalf("maybeCreateReproductionSmokeRunner returned error: %v", err)
	}
	if kind != "bounded_forward_pass" {
		t.Fatalf("unexpected smoke kind: %q", kind)
	}
	raw, err := os.ReadFile(runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(raw); !strings.Contains(text, "nn.Transformer") || !strings.Contains(text, "generic_torch_transformer_smoke") {
		t.Fatalf("generic runner does not contain expected code:\n%s", text)
	}
}

func TestMaybeCreateReproductionSmokeRunner_AttentionAblation(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("Attention Is All You Need"), 0o644); err != nil {
		t.Fatal(err)
	}
	task := &models.Task{
		Name:        "Prepare attention ablation",
		Description: "Run an ablation for heads=1/2/4/8, attention scaling, and residual connection.",
	}

	runnerPath, kind, err := maybeCreateReproductionSmokeRunner(workspace, task)
	if err != nil {
		t.Fatalf("maybeCreateReproductionSmokeRunner returned error: %v", err)
	}
	if kind != "attention_structure_ablation" {
		t.Fatalf("unexpected runner kind: %q", kind)
	}
	raw, err := os.ReadFile(runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{"heads_1", "heads_8", "no_scaling", "no_residual", "attention_structure_ablation"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("ablation runner missing %q", expected)
		}
	}
}

func TestAttentionAblationRunnerUsesSelectedTreeBranches(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("Attention Is All You Need"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := models.AblationPlan{Selected: []models.AblationCandidate{{ID: "data", Category: "data_scale"}}}
	rawPlan, _ := json.Marshal(plan)
	task := &models.Task{Inputs: map[string]any{"ablation_plan": string(rawPlan)}}
	runnerPath, _, err := maybeCreateReproductionSmokeRunner(workspace, task)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(runnerPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, `"sequence_8"`) || !strings.Contains(text, `"sequence_32"`) {
		t.Fatalf("selected data-scale variants are missing")
	}
	if strings.Contains(text, `"no_residual"`) || strings.Contains(text, `"heads_8"`) {
		t.Fatalf("runner contains pruned variants")
	}
}

func TestWorkspaceMatchesRepoURL(t *testing.T) {
	workspace := t.TempDir()
	gitDir := filepath.Join(workspace, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config := `[remote "origin"]
	url = https://github.com/harvardnlp/annotated-transformer.git
`
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if !workspaceMatchesRepoURL(workspace, "https://github.com/harvardnlp/annotated-transformer") {
		t.Fatalf("expected workspace to match repo URL")
	}
	if workspaceMatchesRepoURL(workspace, "https://github.com/example/other") {
		t.Fatalf("did not expect workspace to match unrelated repo URL")
	}
}

func TestMaterializeUploadedFilesRejectsScholarSymlink(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, ".scholar")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(source, []byte("text,label\na,b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task := &models.Task{Inputs: map[string]any{
		"uploaded_files": []map[string]any{{"name": "data.csv", "storage_path": source}},
	}}
	if _, err := materializeUploadedFiles(workspace, task); err == nil {
		t.Fatal("expected unsafe .scholar symlink to be rejected")
	}
	if _, err := os.Stat(filepath.Join(outside, "uploads", "01-data.csv")); !os.IsNotExist(err) {
		t.Fatalf("uploaded file escaped workspace through symlink: %v", err)
	}
}
