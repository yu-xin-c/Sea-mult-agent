package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

const (
	benchmarkAdapterDirectory  = ".scholar/benchmark"
	benchmarkAdapterFile       = ".scholar/benchmark/adapter.py"
	benchmarkAdapterSpecFile   = ".scholar/benchmark/benchmark.json"
	benchmarkMaxContextFiles   = 12
	benchmarkMaxContextPerFile = 12 * 1024
	benchmarkMaxContextBytes   = 80 * 1024
)

type benchmarkAdapterGenerationResponse struct {
	Status       string   `json:"status"`
	Strategy     string   `json:"strategy"`
	EntryPoint   string   `json:"entrypoint"`
	Confidence   float64  `json:"confidence"`
	Metrics      []string `json:"metrics"`
	Dependencies []string `json:"dependencies"`
	Reason       string   `json:"reason"`
	AdapterCode  string   `json:"adapter_code"`
}

type benchmarkAdapterRepairResponse struct {
	AdapterCode string `json:"adapter_code"`
	Reason      string `json:"reason"`
}

func (a *ResearchCodingAgent) executeAdapterGeneration(ctx context.Context, task *models.Task) error {
	if a == nil || a.ChatModel == nil {
		return failResearchCodingTask(task, fmt.Errorf("benchmark adapter model is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	manifest, manifestJSON, err := benchmarkDatasetManifestFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if manifest.RequiresConfirmation {
		return failResearchCodingTask(task, fmt.Errorf("dataset column mapping is ambiguous; provide input and label columns explicitly"))
	}
	repositoryContext, err := collectBenchmarkRepositoryContext(workspacePath, benchmarkTaskString(task, "repo_manifest"))
	if err != nil {
		return failResearchCodingTask(task, err)
	}

	planMessage, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.BenchmarkAdapterSystemPrompt},
		{Role: schema.User, Content: prompts.BenchmarkAdapterPlanUserPrompt(repositoryContext, manifestJSON, task.Description)},
	})
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("adapter strategy selection failed: %w", err))
	}
	plan, err := parseBenchmarkAdapterPlan(planMessage.Content)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if plan.Status != "ready" {
		return failResearchCodingTask(task, fmt.Errorf("repository adapter unsupported: %s", plan.Reason))
	}
	planJSON, _ := json.Marshal(plan)

	generationMessage, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.BenchmarkAdapterSystemPrompt},
		{Role: schema.User, Content: prompts.BenchmarkAdapterGenerationUserPrompt(repositoryContext, manifestJSON, string(planJSON), task.Description)},
	})
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("adapter code generation failed: %w", err))
	}
	generation, err := parseBenchmarkAdapterGeneration(generationMessage.Content)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if generation.Status != "ready" {
		return failResearchCodingTask(task, fmt.Errorf("repository adapter unsupported: %s", generation.Reason))
	}
	if err := validateBenchmarkAdapterCode(generation.AdapterCode); err != nil {
		return failResearchCodingTask(task, err)
	}

	codeHash := sha256.Sum256([]byte(generation.AdapterCode))
	spec := models.BenchmarkAdapterSpec{
		Version:           "benchmark.adapter/v1",
		Status:            "generated",
		Strategy:          sanitizeBenchmarkStrategy(generation.Strategy),
		EntryPoint:        strings.TrimSpace(generation.EntryPoint),
		Confidence:        clampUnit(generation.Confidence),
		DatasetSHA256:     manifest.SHA256,
		InputColumn:       manifest.InputColumn,
		TargetColumn:      manifest.TargetColumn,
		Metrics:           cleanBenchmarkStrings(generation.Metrics, 12),
		Dependencies:      cleanBenchmarkStrings(generation.Dependencies, 24),
		AdapterCodeSHA256: hex.EncodeToString(codeHash[:]),
		Reason:            strings.TrimSpace(generation.Reason),
	}
	if spec.Strategy == "" {
		spec.Strategy = plan.Candidates[plan.SelectedIndex].Kind
	}
	if spec.EntryPoint == "" {
		spec.EntryPoint = plan.Candidates[plan.SelectedIndex].EntryPoint
	}
	if len(spec.Metrics) == 0 {
		spec.Metrics = defaultBenchmarkMetrics(manifest.SuggestedTask)
	}
	specJSON, _ := json.Marshal(spec)
	adapterPath, specPath, err := writeBenchmarkAdapterFiles(workspacePath, generation.AdapterCode, specJSON)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	relAdapter, _ := filepath.Rel(workspacePath, adapterPath)
	relSpec, _ := filepath.Rel(workspacePath, specPath)
	report := fmt.Sprintf("selected %s adapter at %s with confidence %.2f", spec.Strategy, spec.EntryPoint, spec.Confidence)

	task.Code = generation.AdapterCode
	task.Result = report
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"benchmark_adapter_plan":      string(planJSON),
		"benchmark_adapter_spec":      string(specJSON),
		"benchmark_generated_code":    generation.AdapterCode,
		"benchmark_code_file_path":    adapterPath,
		"benchmark_adapter_report":    report,
		"benchmark_adapter_spec_path": filepath.ToSlash(relSpec),
	})
	logToContext(ctx, "[%s] generated adapter %s", a.Name, filepath.ToSlash(relAdapter))
	return nil
}

func parseBenchmarkAdapterPlan(raw string) (models.BenchmarkAdapterPlan, error) {
	var plan models.BenchmarkAdapterPlan
	if err := json.Unmarshal([]byte(cleanJSONResponse(raw)), &plan); err != nil {
		return plan, fmt.Errorf("parse adapter strategy plan: %w", err)
	}
	plan.Status = strings.ToLower(strings.TrimSpace(plan.Status))
	if plan.Status != "ready" && plan.Status != "unsupported" {
		return plan, fmt.Errorf("adapter strategy returned invalid status %q", plan.Status)
	}
	if len(plan.Candidates) > 3 {
		plan.Candidates = plan.Candidates[:3]
	}
	if plan.Status == "ready" && len(plan.Candidates) == 0 {
		return plan, fmt.Errorf("adapter strategy returned no candidates")
	}
	for index := range plan.Candidates {
		plan.Candidates[index].Kind = sanitizeBenchmarkStrategy(plan.Candidates[index].Kind)
		plan.Candidates[index].EntryPoint = strings.TrimSpace(plan.Candidates[index].EntryPoint)
		plan.Candidates[index].Confidence = clampUnit(plan.Candidates[index].Confidence)
		if plan.Candidates[index].Kind == "" || plan.Candidates[index].EntryPoint == "" {
			return plan, fmt.Errorf("adapter strategy candidate %d is incomplete", index)
		}
	}
	if plan.SelectedIndex < 0 || plan.SelectedIndex >= len(plan.Candidates) {
		plan.SelectedIndex = 0
	}
	return plan, nil
}

func parseBenchmarkAdapterGeneration(raw string) (benchmarkAdapterGenerationResponse, error) {
	var response benchmarkAdapterGenerationResponse
	if err := json.Unmarshal([]byte(cleanJSONResponse(raw)), &response); err != nil {
		return response, fmt.Errorf("parse generated benchmark adapter: %w", err)
	}
	response.Status = strings.ToLower(strings.TrimSpace(response.Status))
	if response.Status != "ready" && response.Status != "unsupported" {
		return response, fmt.Errorf("generated benchmark adapter returned invalid status %q", response.Status)
	}
	response.AdapterCode = strings.TrimSpace(response.AdapterCode)
	return response, nil
}

func validateBenchmarkAdapterCode(code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("generated benchmark adapter is empty")
	}
	if len(code) > 200*1024 {
		return fmt.Errorf("generated benchmark adapter exceeds 200 KiB")
	}
	for _, required := range []string{"--dataset", "--output-dir", "--limit", "--repo-root", "metrics.json", "predictions.jsonl", "run_manifest.json", "dataset_sha256"} {
		if !strings.Contains(code, required) {
			return fmt.Errorf("generated benchmark adapter is missing contract token %q", required)
		}
	}
	lower := strings.ToLower(code)
	for _, forbidden := range []string{
		"os.remove(", "os.unlink(", ".unlink(", "shutil.rmtree(", "shell=true", "pip install",
		"os.system(", "os.popen(", "subprocess.", "requests.", "httpx.", "aiohttp.", "urllib.request", "urllib3.", "socket.socket(", "torch.hub.",
		"random prediction", "dummy prediction", "fake metric",
	} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("generated benchmark adapter violates policy: %s", forbidden)
		}
	}
	return nil
}

func writeBenchmarkAdapterFiles(workspacePath, code string, specJSON []byte) (string, string, error) {
	directory, err := benchmarkPathInWorkspace(workspacePath, benchmarkAdapterDirectory)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", err
	}
	adapterPath, err := benchmarkPathInWorkspace(workspacePath, benchmarkAdapterFile)
	if err != nil {
		return "", "", err
	}
	specPath, err := benchmarkPathInWorkspace(workspacePath, benchmarkAdapterSpecFile)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(adapterPath, []byte(code), 0o600); err != nil {
		return "", "", fmt.Errorf("write benchmark adapter: %w", err)
	}
	if err := os.WriteFile(specPath, specJSON, 0o600); err != nil {
		return "", "", fmt.Errorf("write benchmark adapter spec: %w", err)
	}
	return adapterPath, specPath, nil
}

func collectBenchmarkRepositoryContext(workspacePath, repoManifest string) (string, error) {
	type contextFile struct {
		path     string
		priority int
	}
	candidates := make([]contextFile, 0, 64)
	err := filepath.WalkDir(workspacePath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if name == ".git" || name == ".scholar" || name == "node_modules" || name == ".venv" || name == "venv" || name == "__pycache__" {
				return filepath.SkipDir
			}
			if strings.Count(filepath.ToSlash(relative), "/") >= 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		priority, ok := benchmarkContextFilePriority(relative)
		if !ok {
			return nil
		}
		candidates = append(candidates, contextFile{path: path, priority: priority})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("scan repository for benchmark entry points: %w", err)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].priority == candidates[j].priority {
			return candidates[i].path < candidates[j].path
		}
		return candidates[i].priority < candidates[j].priority
	})

	var builder strings.Builder
	if strings.TrimSpace(repoManifest) != "" {
		builder.WriteString("Repository manifest:\n")
		builder.WriteString(truncateBenchmarkText(repoManifest, 12*1024))
		builder.WriteString("\n\n")
	}
	total := 0
	fileCount := 0
	for _, candidate := range candidates {
		if total >= benchmarkMaxContextBytes || fileCount >= benchmarkMaxContextFiles {
			break
		}
		raw, err := os.ReadFile(candidate.path)
		if err != nil {
			continue
		}
		if len(raw) > benchmarkMaxContextPerFile {
			raw = raw[:benchmarkMaxContextPerFile]
		}
		if remaining := benchmarkMaxContextBytes - total; len(raw) > remaining {
			raw = raw[:remaining]
		}
		relative, _ := filepath.Rel(workspacePath, candidate.path)
		builder.WriteString("--- FILE ")
		builder.WriteString(filepath.ToSlash(relative))
		builder.WriteString(" ---\n")
		builder.Write(raw)
		builder.WriteString("\n\n")
		total += len(raw)
		fileCount++
	}
	if total == 0 {
		return "", fmt.Errorf("repository contains no readable evaluation or configuration context")
	}
	return builder.String(), nil
}

func benchmarkContextFilePriority(relative string) (int, bool) {
	lower := strings.ToLower(filepath.ToSlash(relative))
	base := filepath.Base(lower)
	if strings.HasPrefix(base, "readme") {
		return 0, true
	}
	switch base {
	case "pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "environment.yml", "environment.yaml":
		return 1, true
	}
	ext := strings.ToLower(filepath.Ext(base))
	if ext != ".py" && ext != ".yaml" && ext != ".yml" && ext != ".json" && ext != ".toml" {
		return 0, false
	}
	for _, marker := range []string{"eval", "test", "infer", "predict", "benchmark", "model", "trainer", "config"} {
		if strings.Contains(lower, marker) {
			return 2, true
		}
	}
	return 0, false
}

func benchmarkWorkspace(task *models.Task) (string, error) {
	workspacePath := filepath.Clean(strings.TrimSpace(extractTaskInputLike(task, "workspace_path")))
	if workspacePath == "" || workspacePath == "." {
		return "", fmt.Errorf("workspace_path input is required")
	}
	workspacePath, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	workspacePath, err = filepath.EvalSymlinks(workspacePath)
	if err != nil {
		return "", fmt.Errorf("workspace_path is unavailable")
	}
	info, err := os.Stat(workspacePath)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace_path is unavailable")
	}
	return workspacePath, nil
}

func benchmarkPathInWorkspace(workspacePath, relative string) (string, error) {
	workspaceAbs, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", err
	}
	targetAbs, err := filepath.Abs(filepath.Join(workspaceAbs, filepath.FromSlash(relative)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(workspaceAbs, targetAbs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("benchmark path escapes workspace: %s", relative)
	}
	current := workspaceAbs
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	for index, part := range parts {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("benchmark path contains symlink: %s", filepath.ToSlash(current))
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("benchmark path component is not a directory: %s", filepath.ToSlash(current))
		}
	}
	return targetAbs, nil
}

func benchmarkDatasetManifestFromTask(task *models.Task) (models.DatasetManifest, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "dataset_manifest"))
	if raw == "" {
		return models.DatasetManifest{}, "", fmt.Errorf("dataset_manifest input is required")
	}
	var manifest models.DatasetManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return manifest, raw, fmt.Errorf("decode dataset manifest: %w", err)
	}
	if manifest.SHA256 == "" || manifest.Name == "" || manifest.RowCount <= 0 {
		return manifest, raw, fmt.Errorf("dataset manifest is incomplete")
	}
	return manifest, raw, nil
}

func benchmarkAdapterSpecFromTask(task *models.Task) (models.BenchmarkAdapterSpec, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_adapter_spec"))
	if raw == "" {
		raw = strings.TrimSpace(extractTaskInputLike(task, "validated_benchmark_adapter_spec"))
	}
	if raw == "" {
		return models.BenchmarkAdapterSpec{}, "", fmt.Errorf("benchmark adapter spec input is required")
	}
	var spec models.BenchmarkAdapterSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return spec, raw, fmt.Errorf("decode benchmark adapter spec: %w", err)
	}
	return spec, raw, nil
}

func sanitizeBenchmarkStrategy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "native_eval", "framework_api", "import_wrapper":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return ""
	}
}

func cleanBenchmarkStrings(values []string, limit int) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func defaultBenchmarkMetrics(taskType string) []string {
	switch taskType {
	case "classification":
		return []string{"accuracy", "macro_f1"}
	case "regression":
		return []string{"mse", "mae"}
	default:
		return []string{"latency_ms", "throughput"}
	}
}

func truncateBenchmarkText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func benchmarkMinInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
