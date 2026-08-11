package scheduler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

func TestWorkspaceMatchesRepoURLFromArchiveMarker(t *testing.T) {
	workspace := t.TempDir()
	if err := writeRepositorySourceMarker(workspace, repositorySourceMarker{
		RepoURL:     "https://github.com/mem0ai/mem0",
		Commit:      "4debc58a83377b18be81ae1e5969a300736b2fac",
		Acquisition: "github_codeload_archive",
	}); err != nil {
		t.Fatal(err)
	}
	if !workspaceMatchesRepoURL(workspace, "https://github.com/mem0ai/mem0.git") {
		t.Fatal("expected archive-backed workspace to match repository URL")
	}
	if workspaceMatchesRepoURL(workspace, "https://github.com/microsoft/graphrag") {
		t.Fatal("did not expect archive-backed workspace to match another URL")
	}
}

func TestCloneCachedGitRepositoryUsesCommittedTree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	source := t.TempDir()
	runGitTestCommand(t, source, "init")
	runGitTestCommand(t, source, "config", "user.email", "tests@example.com")
	runGitTestCommand(t, source, "config", "user.name", "ScholarAgent Tests")
	trackedPath := filepath.Join(source, "candidate.py")
	if err := os.WriteFile(trackedPath, []byte("VALUE = 'committed'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, source, "add", "candidate.py")
	runGitTestCommand(t, source, "commit", "-m", "fixture")
	if err := writeRepositorySourceMarker(source, repositorySourceMarker{
		RepoURL: "https://github.com/example/research", Acquisition: "git_shallow_clone",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trackedPath, []byte("VALUE = 'dirty'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, ".scholar", "uploads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".scholar", "uploads", "01-old.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	cloned, err := cloneCachedGitRepository(context.Background(), "https://github.com/example/research", source, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cloned) })
	raw, err := os.ReadFile(filepath.Join(cloned, "candidate.py"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "VALUE = 'committed'\n" {
		t.Fatalf("cached clone inherited dirty worktree: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(cloned, ".scholar")); !os.IsNotExist(err) {
		t.Fatalf("cached clone inherited task uploads: %v", err)
	}
	marker, err := readRepositorySourceMarker(cloned)
	if err != nil {
		t.Fatal(err)
	}
	if marker.Acquisition != "git_local_cache_clone" || marker.Commit == "" {
		t.Fatalf("unexpected cloned source marker: %#v", marker)
	}
}

func TestRepoPrepareRepositoryRevisionFromUploadedAutoResearchSpec(t *testing.T) {
	revision := "47aa3ddf8dc1ebeb7ef4e65f2b4536af44594099"
	specPath := filepath.Join(t.TempDir(), "autoresearch.json")
	if err := os.WriteFile(specPath, []byte(`{"version":"autoresearch.spec/v1","repository_revision":"`+revision+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	task := &models.Task{Inputs: map[string]any{
		"uploaded_files": []map[string]any{{"name": "autoresearch.json", "storage_path": specPath}},
	}}
	got, err := repoPrepareRepositoryRevision(task)
	if err != nil {
		t.Fatal(err)
	}
	if got != revision {
		t.Fatalf("repository revision=%q, want %q", got, revision)
	}

	task.Inputs["repository_revision"] = "14a00ad88fc33cf2b52f4f113f25807556f8e25e"
	if _, err := repoPrepareRepositoryRevision(task); err == nil {
		t.Fatal("expected conflicting explicit and uploaded revisions to be rejected")
	}
}

func TestCloneRepositoryWithRetryChecksOutPinnedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	source := t.TempDir()
	runGitTestCommand(t, source, "init")
	runGitTestCommand(t, source, "config", "user.email", "tests@example.com")
	runGitTestCommand(t, source, "config", "user.name", "ScholarAgent Tests")
	trackedPath := filepath.Join(source, "candidate.py")
	if err := os.WriteFile(trackedPath, []byte("VALUE = 'first'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, source, "add", "candidate.py")
	runGitTestCommand(t, source, "commit", "-m", "first")
	firstCommit := runGitTestOutput(t, source, "rev-parse", "HEAD")
	if err := os.WriteFile(trackedPath, []byte("VALUE = 'second'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTestCommand(t, source, "commit", "-am", "second")

	workspace := filepath.Join(t.TempDir(), "checkout")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	attempts := []string{}
	if err := cloneRepositoryWithRetry(context.Background(), source, workspace, firstCommit, &attempts); err != nil {
		t.Fatalf("clone pinned revision failed: %v; attempts=%v", err, attempts)
	}
	raw, err := os.ReadFile(filepath.Join(workspace, "candidate.py"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "VALUE = 'first'\n" {
		t.Fatalf("pinned checkout used the moving HEAD: %q", raw)
	}
	marker, err := readRepositorySourceMarker(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if marker.Commit != firstCommit || marker.Acquisition != "git_pinned_fetch" {
		t.Fatalf("unexpected pinned source marker: %#v", marker)
	}
}

func runGitTestCommand(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v (%s)", arguments, err, output)
	}
}

func runGitTestOutput(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v (%s)", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func TestParseGitHubRepository(t *testing.T) {
	repository, err := parseGitHubRepository("https://github.com/microsoft/graphrag.git")
	if err != nil {
		t.Fatal(err)
	}
	if repository.Owner != "microsoft" || repository.Name != "graphrag" {
		t.Fatalf("unexpected repository: %#v", repository)
	}
	for _, unsafeURL := range []string{
		"https://gitlab.com/microsoft/graphrag",
		"https://github.com/microsoft/graphrag/tree/main",
		"https://user@github.com/microsoft/graphrag",
		"https://github.com/microsoft/graphrag?download=1",
	} {
		if _, err := parseGitHubRepository(unsafeURL); err == nil {
			t.Fatalf("expected %q to be rejected", unsafeURL)
		}
	}
}

func TestExtractRepositoryArchive(t *testing.T) {
	archive := buildRepositoryArchive(t, []tar.Header{
		{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "test-commit"}},
		{Name: "repo-commit/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "repo-commit/README.md", Typeflag: tar.TypeReg, Mode: 0o644, Size: 6},
		{Name: "repo-commit/bin/run.sh", Typeflag: tar.TypeReg, Mode: 0o755, Size: 8},
		{Name: "repo-commit/CLAUDE.md", Typeflag: tar.TypeSymlink, Linkname: "README.md", Mode: 0o777},
	}, [][]byte{nil, nil, []byte("hello\n"), []byte("echo ok\n"), nil})
	workspace := t.TempDir()
	if err := extractRepositoryArchive(bytes.NewReader(archive), workspace); err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join(workspace, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(readme) != "hello\n" {
		t.Fatalf("unexpected README: %q", readme)
	}
	info, err := os.Stat(filepath.Join(workspace, "bin", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("expected executable bit to be preserved")
	}
	linkTarget, err := os.Readlink(filepath.Join(workspace, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != "README.md" {
		t.Fatalf("unexpected symlink target: %q", linkTarget)
	}
}

func TestExtractRepositoryArchiveRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name   string
		header tar.Header
	}{
		{name: "traversal", header: tar.Header{Name: "repo/../../escape", Typeflag: tar.TypeReg, Mode: 0o644}},
		{name: "absolute_symlink", header: tar.Header{Name: "repo/link", Typeflag: tar.TypeSymlink, Linkname: "/tmp", Mode: 0o777}},
		{name: "escaping_symlink", header: tar.Header{Name: "repo/nested/link", Typeflag: tar.TypeSymlink, Linkname: "../../../tmp", Mode: 0o777}},
		{name: "absolute", header: tar.Header{Name: "/repo/file", Typeflag: tar.TypeReg, Mode: 0o644}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := buildRepositoryArchive(t, []tar.Header{test.header}, [][]byte{nil})
			if err := extractRepositoryArchive(bytes.NewReader(archive), t.TempDir()); err == nil {
				t.Fatal("expected unsafe archive entry to be rejected")
			}
		})
	}
}

func TestExtractRepositoryArchiveFixture(t *testing.T) {
	fixturePath := strings.TrimSpace(os.Getenv("REPO_ARCHIVE_FIXTURE"))
	if fixturePath == "" {
		t.Skip("REPO_ARCHIVE_FIXTURE is not configured")
	}
	expectedPath := strings.TrimSpace(os.Getenv("REPO_ARCHIVE_EXPECTED_PATH"))
	if expectedPath == "" {
		expectedPath = "README.md"
	}
	fixture, err := os.Open(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	defer fixture.Close()
	workspace := t.TempDir()
	if err := extractRepositoryArchive(fixture, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(workspace, filepath.FromSlash(expectedPath))); err != nil {
		t.Fatalf("expected extracted path %q: %v", expectedPath, err)
	}
}

func buildRepositoryArchive(t *testing.T, headers []tar.Header, bodies [][]byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for index := range headers {
		header := headers[index]
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if index < len(bodies) && len(bodies[index]) > 0 {
			if _, err := tarWriter.Write(bodies[index]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
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
