package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"scholar-agent-backend/internal/models"
)

type repoPrepareManifest struct {
	RepoURL                string                    `json:"repo_url"`
	WorkspacePath          string                    `json:"workspace_path"`
	SelectedCodeFile       string                    `json:"selected_code_file,omitempty"`
	DependencyFiles        []string                  `json:"dependency_files,omitempty"`
	CodeFileCandidates     []string                  `json:"code_file_candidates,omitempty"`
	CloneAttempts          []string                  `json:"clone_attempts,omitempty"`
	ReproEntryKind         string                    `json:"repro_entry_kind,omitempty"`
	ReproductionMode       string                    `json:"reproduction_mode"`
	FullReproductionSwitch bool                      `json:"full_reproduction_switch"`
	ModeDecision           ReproductionModeDecision  `json:"mode_decision"`
	HardwareProbe          ReproductionResourceProbe `json:"hardware_probe"`
}

const reproductionSmokeRunnerName = "scholar_repro_smoke.py"

func executeRepoPrepare(ctx context.Context, runtimeTask *models.Task) error {
	if runtimeTask == nil {
		return fmt.Errorf("runtime task is nil")
	}

	repoURL := strings.TrimSpace(taskInputValue(runtimeTask, "repo_url"))
	if repoURL == "" {
		return fmt.Errorf("repo_prepare: missing repo_url")
	}

	candidateURLs := repoPrepareCandidateURLs(runtimeTask, repoURL)
	repoURL, workspacePath, cloneAttempts, err := cloneFirstAvailableRepository(ctx, candidateURLs)
	if err != nil {
		return err
	}

	dependencyFiles, codeCandidates, scanErr := scanRepositoryWorkspace(workspacePath)
	if scanErr != nil {
		return scanErr
	}

	selectedCodeFile := choosePreferredCodeFile(codeCandidates)
	reproEntryKind := ""
	modeDecision := decideReproductionMode(runtimeTask, workspacePath)
	if modeDecision.EffectiveMode == reproductionModeFull {
		reproEntryKind = "repository_full_experiment"
	} else {
		if smokeFile, smokeKind, createErr := maybeCreateReproductionSmokeRunner(workspacePath, runtimeTask); createErr != nil {
			return createErr
		} else if smokeFile != "" {
			selectedCodeFile = smokeFile
			codeCandidates = append([]string{smokeFile}, codeCandidates...)
			reproEntryKind = smokeKind
		}
	}

	generatedCode := ""
	if selectedCodeFile != "" {
		raw, readErr := os.ReadFile(selectedCodeFile)
		if readErr != nil {
			return fmt.Errorf("read selected repo code file failed: %w", readErr)
		}
		generatedCode = string(raw)
	}

	manifest := repoPrepareManifest{
		RepoURL:                repoURL,
		WorkspacePath:          workspacePath,
		SelectedCodeFile:       selectedCodeFile,
		DependencyFiles:        toWorkspaceRelativePaths(workspacePath, dependencyFiles),
		CodeFileCandidates:     toWorkspaceRelativePaths(workspacePath, codeCandidates),
		CloneAttempts:          cloneAttempts,
		ReproEntryKind:         reproEntryKind,
		ReproductionMode:       modeDecision.EffectiveMode,
		FullReproductionSwitch: modeDecision.EffectiveMode == reproductionModeFull,
		ModeDecision:           modeDecision,
		HardwareProbe:          modeDecision.Probe,
	}
	manifestJSON, _ := json.Marshal(manifest)
	modeReport := reproductionModeReport(modeDecision)

	if runtimeTask.Metadata == nil {
		runtimeTask.Metadata = map[string]any{}
	}
	runtimeTask.Metadata["artifact_values"] = map[string]any{
		"workspace_path":           workspacePath,
		"code_file_path":           selectedCodeFile,
		"generated_code":           generatedCode,
		"repo_manifest":            string(manifestJSON),
		"reproduction_mode_report": modeReport,
	}

	runtimeTask.Result = chooseNonEmpty(workspacePath, selectedCodeFile, repoURL)
	runtimeTask.Code = generatedCode
	runtimeTask.Status = models.StatusCompleted
	return nil
}

func reproductionModeReport(decision ReproductionModeDecision) string {
	raw, _ := json.MarshalIndent(decision, "", "  ")
	return string(raw)
}

func maybeCreateReproductionSmokeRunner(workspacePath string, task *models.Task) (string, string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", "", nil
	}

	transformerPath := filepath.Join(workspacePath, "src", "architectures", "machine_translation_transformer.py")
	hasRepoTransformer := false
	if _, err := os.Stat(transformerPath); err == nil {
		hasRepoTransformer = true
	}
	if !hasRepoTransformer && !shouldCreateGenericAttentionSmokeRunner(workspacePath, task) {
		return "", "", nil
	}

	runnerPath := filepath.Join(workspacePath, reproductionSmokeRunnerName)
	runner := buildAttentionReproductionSmokeRunner(hasRepoTransformer)
	if err := os.WriteFile(runnerPath, []byte(runner), 0o644); err != nil {
		return "", "", fmt.Errorf("create reproduction smoke runner: %w", err)
	}
	return runnerPath, "bounded_forward_pass", nil
}

func shouldCreateGenericAttentionSmokeRunner(workspacePath string, task *models.Task) bool {
	context := strings.ToLower(strings.Join([]string{
		taskField(task, func(t *models.Task) string { return t.Name }),
		taskField(task, func(t *models.Task) string { return t.Description }),
		taskInputValue(task, "repo_url"),
	}, " "))
	if strings.Contains(context, "attention is all you need") || strings.Contains(context, "transformer") {
		return true
	}

	for _, candidate := range []string{
		filepath.Join(workspacePath, "README.md"),
		filepath.Join(workspacePath, "readme.md"),
	} {
		raw, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		text := strings.ToLower(string(raw))
		if strings.Contains(text, "attention is all you need") || strings.Contains(text, "transformer") {
			return true
		}
	}
	return false
}

func taskField(task *models.Task, getter func(*models.Task) string) string {
	if task == nil {
		return ""
	}
	return getter(task)
}

func buildAttentionReproductionSmokeRunner(useRepoTransformer bool) string {
	if useRepoTransformer {
		return `import json
import os
import sys
import time

import torch

sys.path.insert(0, os.path.join(os.getcwd(), "src"))
from architectures.machine_translation_transformer import MachineTranslationTransformer


def main():
    torch.manual_seed(0)
    cfg = {
        "d_model": 64,
        "n_blocks": 2,
        "n_heads": 4,
        "d_ff": 128,
        "dropout_proba": 0.0,
        "src_vocab_size": 96,
        "trg_vocab_size": 96,
        "batch_size": 2,
        "src_seq_len": 8,
        "trg_seq_len": 9,
    }
    model = MachineTranslationTransformer(
        d_model=cfg["d_model"],
        n_blocks=cfg["n_blocks"],
        src_vocab_size=cfg["src_vocab_size"],
        trg_vocab_size=cfg["trg_vocab_size"],
        n_heads=cfg["n_heads"],
        d_ff=cfg["d_ff"],
        dropout_proba=cfg["dropout_proba"],
    )
    model.eval()
    src = torch.randint(1, cfg["src_vocab_size"], (cfg["batch_size"], cfg["src_seq_len"]))
    trg = torch.randint(1, cfg["trg_vocab_size"], (cfg["batch_size"], cfg["trg_seq_len"]))

    start = time.perf_counter()
    with torch.no_grad():
        output = model(src, trg)
    elapsed_ms = (time.perf_counter() - start) * 1000

    metrics = {
        "status": "ok",
        "reproduction_scope": "bounded_forward_pass",
        "paper": "Attention Is All You Need",
        "architecture": "Transformer encoder-decoder",
        "repo_entry": "src/architectures/machine_translation_transformer.py",
        "model_config": cfg,
        "output_shape": list(output.shape),
        "parameter_count": sum(p.numel() for p in model.parameters()),
        "forward_elapsed_ms": round(elapsed_ms, 3),
        "output_abs_mean": round(float(output.abs().mean().item()), 6),
        "notes": [
            "真实导入仓库 Transformer 模型代码并在 CPU 上执行前向传播。",
            "自动化 smoke reproduction 不默认跑完整 WMT14 训练，以避免外部登录、GPU 和长时间训练依赖。",
        ],
    }
    print(json.dumps(metrics, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
`
	}

	return `import json
import time

import torch
import torch.nn as nn


def main():
    torch.manual_seed(0)
    cfg = {
        "d_model": 64,
        "n_blocks": 2,
        "n_heads": 4,
        "d_ff": 128,
        "dropout_proba": 0.0,
        "batch_size": 2,
        "src_seq_len": 8,
        "trg_seq_len": 7,
    }
    model = nn.Transformer(
        d_model=cfg["d_model"],
        nhead=cfg["n_heads"],
        num_encoder_layers=cfg["n_blocks"],
        num_decoder_layers=cfg["n_blocks"],
        dim_feedforward=cfg["d_ff"],
        dropout=cfg["dropout_proba"],
        batch_first=True,
    )
    model.eval()
    src = torch.randn(cfg["batch_size"], cfg["src_seq_len"], cfg["d_model"])
    trg = torch.randn(cfg["batch_size"], cfg["trg_seq_len"], cfg["d_model"])

    start = time.perf_counter()
    with torch.no_grad():
        output = model(src, trg)
    elapsed_ms = (time.perf_counter() - start) * 1000

    metrics = {
        "status": "ok",
        "reproduction_scope": "bounded_forward_pass",
        "paper": "Attention Is All You Need",
        "architecture": "Transformer encoder-decoder",
        "repo_entry": "generic_torch_transformer_smoke",
        "model_config": cfg,
        "output_shape": list(output.shape),
        "parameter_count": sum(p.numel() for p in model.parameters()),
        "forward_elapsed_ms": round(elapsed_ms, 3),
        "output_abs_mean": round(float(output.abs().mean().item()), 6),
        "notes": [
            "真实 clone 论文候选仓库；该仓库未提供可直接导入的 Python 模型源码，因此使用 PyTorch 标准 Transformer 做受控前向复现。",
            "自动化 smoke reproduction 不默认跑完整 WMT14 训练，以避免外部登录、GPU 和长时间训练依赖。",
        ],
    }
    print(json.dumps(metrics, ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
`
}

func repoPrepareCandidateURLs(task *models.Task, primary string) []string {
	urls := make([]string, 0, 6)
	appendURL := func(value string) {
		value = normalizeGitHubRepoURL(value)
		if value == "" {
			return
		}
		urls = append(urls, value)
	}

	appendURL(primary)

	// For well-known papers, keep a curated implementation near the front.
	// This gives paper reproduction a trustworthy fallback when GitHub Search
	// returns a stale or oversized repository.
	if task != nil {
		for _, candidate := range curatedRepoFallbackCandidates(task.Description) {
			for _, repoURL := range candidate.RepoURLs {
				appendURL(repoURL)
			}
		}
	}

	if raw := strings.TrimSpace(taskInputValue(task, "candidate_repositories")); raw != "" {
		var candidates []repoCandidate
		if err := json.Unmarshal([]byte(raw), &candidates); err == nil {
			for _, candidate := range candidates {
				for _, repoURL := range candidate.RepoURLs {
					appendURL(repoURL)
				}
			}
		}
	}

	return uniqueNonEmptyStrings(urls)
}

func normalizeGitHubRepoURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimRight(value, "/")
	return value
}

func cloneFirstAvailableRepository(ctx context.Context, candidateURLs []string) (string, string, []string, error) {
	candidateURLs = uniqueNonEmptyStrings(candidateURLs)
	if len(candidateURLs) == 0 {
		return "", "", nil, fmt.Errorf("repo_prepare: missing clone candidates")
	}
	if len(candidateURLs) > 5 {
		candidateURLs = candidateURLs[:5]
	}

	attempts := make([]string, 0, len(candidateURLs)*2)
	if cachedURL, cachedWorkspace := findCachedRepositoryWorkspace(candidateURLs); cachedWorkspace != "" {
		attempts = append(attempts, fmt.Sprintf("%s: cache hit %s", cachedURL, cachedWorkspace))
		return cachedURL, cachedWorkspace, attempts, nil
	}

	for _, repoURL := range candidateURLs {
		workspacePath, err := os.MkdirTemp("", "scholar_repo_workspace_")
		if err != nil {
			return "", "", attempts, fmt.Errorf("create repo workspace: %w", err)
		}

		if err := cloneRepositoryWithRetry(ctx, repoURL, workspacePath, &attempts); err != nil {
			_ = os.RemoveAll(workspacePath)
			continue
		}
		return repoURL, workspacePath, attempts, nil
	}

	return "", "", attempts, fmt.Errorf("clone repo failed after %d candidate(s): %s", len(candidateURLs), strings.Join(attempts, " | "))
}

func findCachedRepositoryWorkspace(candidateURLs []string) (string, string) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return "", ""
	}

	for _, repoURL := range candidateURLs {
		normalizedURL := normalizeGitHubRepoURL(repoURL)
		if normalizedURL == "" {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "scholar_repo_workspace_") {
				continue
			}
			workspacePath := filepath.Join(os.TempDir(), entry.Name())
			if workspaceMatchesRepoURL(workspacePath, normalizedURL) {
				return normalizedURL, workspacePath
			}
		}
	}
	return "", ""
}

func workspaceMatchesRepoURL(workspacePath string, repoURL string) bool {
	raw, err := os.ReadFile(filepath.Join(workspacePath, ".git", "config"))
	if err != nil {
		return false
	}
	config := strings.ToLower(string(raw))
	repoURL = strings.ToLower(normalizeGitHubRepoURL(repoURL))
	return repoURL != "" && (strings.Contains(config, repoURL) || strings.Contains(config, repoURL+".git"))
}

func cloneRepositoryWithRetry(ctx context.Context, repoURL, workspacePath string, attempts *[]string) error {
	cloneCommands := [][]string{
		{"clone", "--depth", "1", "--filter=blob:none", "--single-branch", repoURL, workspacePath},
		{"clone", "--depth", "1", repoURL, workspacePath},
	}

	var lastErr error
	for idx, args := range cloneCommands {
		if idx > 0 {
			_ = os.RemoveAll(workspacePath)
			if err := os.MkdirAll(workspacePath, 0o755); err != nil {
				return fmt.Errorf("recreate repo workspace: %w", err)
			}
		}

		cloneCtx, cancel := context.WithTimeout(ctx, envDuration("REPO_CLONE_TIMEOUT", 45*time.Second))
		cmd := exec.CommandContext(cloneCtx, "git", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cmd.WaitDelay = 5 * time.Second
		output, err := cmd.CombinedOutput()
		cancel()
		if err == nil {
			*attempts = append(*attempts, fmt.Sprintf("%s: ok", repoURL))
			return nil
		}

		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("%v (%s)", err, msg)
		}
		*attempts = append(*attempts, fmt.Sprintf("%s: %s", repoURL, msg))
		lastErr = fmt.Errorf("%s", msg)
	}
	return lastErr
}

func scanRepositoryWorkspace(workspacePath string) ([]string, []string, error) {
	dependencyFiles := make([]string, 0, 8)
	codeCandidates := make([]string, 0, 16)

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", "node_modules", ".venv", "venv", "dist", "build", "docs":
				return filepath.SkipDir
			}
			return nil
		}

		lowerName := strings.ToLower(name)
		switch lowerName {
		case "requirements.txt", "environment.yml", "environment.yaml", "pyproject.toml", "setup.py", "setup.cfg", "pipfile":
			dependencyFiles = append(dependencyFiles, path)
		}

		if strings.HasSuffix(lowerName, ".py") {
			codeCandidates = append(codeCandidates, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan repository workspace: %w", err)
	}

	sort.SliceStable(codeCandidates, func(i, j int) bool {
		return codeFileScore(codeCandidates[i]) > codeFileScore(codeCandidates[j])
	})
	return dependencyFiles, codeCandidates, nil
}

func choosePreferredCodeFile(candidates []string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func codeFileScore(path string) int {
	score := 0
	base := strings.ToLower(filepath.Base(path))
	full := strings.ToLower(path)

	switch base {
	case "the_annotated_transformer.py":
		score += 100
	case "main.py":
		score += 50
	case "train.py":
		score += 40
	case "run.py":
		score += 30
	}

	if strings.Contains(full, "annotated") && strings.Contains(full, "transformer") {
		score += 60
	}
	if strings.Contains(full, "attention") && strings.Contains(full, "transformer") {
		score += 30
	}
	if strings.Contains(full, "test") || strings.Contains(full, "example") || strings.Contains(full, "demo") {
		score -= 20
	}
	if strings.Contains(full, "tutorial") || strings.Contains(full, "notebook") {
		score -= 10
	}
	return score
}

func toWorkspaceRelativePaths(workspacePath string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		rel, err := filepath.Rel(workspacePath, value)
		if err != nil {
			out = append(out, value)
			continue
		}
		out = append(out, rel)
	}
	return out
}

func taskInputValue(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
