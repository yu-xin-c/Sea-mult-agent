package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

const (
	paperDebugMaxRuns         = 3
	paperDebugMaxRepairs      = 2
	paperDebugMaxPatchFiles   = 3
	paperDebugMaxContextFiles = 8
	paperDebugMaxFileBytes    = 96 * 1024
	paperDebugMaxContextBytes = 256 * 1024
	paperDebugPreviewBytes    = 4000
)

var paperDebugTracebackFileRE = regexp.MustCompile(`File ["']([^"']+\.py)["']`)

type paperCodePatchProposal struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

type paperCodeRepairResponse struct {
	Status    string                   `json:"status"`
	Diagnosis string                   `json:"diagnosis"`
	Patches   []paperCodePatchProposal `json:"patches"`
}

type paperCodeRunAttempt struct {
	Run                     int    `json:"run"`
	ExitCode                int    `json:"exit_code"`
	StdoutPreview           string `json:"stdout_preview,omitempty"`
	StderrPreview           string `json:"stderr_preview,omitempty"`
	Error                   string `json:"error,omitempty"`
	SourceFingerprintBefore string `json:"source_fingerprint_before"`
	SourceFingerprintAfter  string `json:"source_fingerprint_after"`
	stdout                  string
	stderr                  string
	images                  []string
}

type paperCodeAppliedPatch struct {
	Repair       int    `json:"repair"`
	Path         string `json:"path"`
	Reason       string `json:"reason"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
}

type paperCodeDebugReport struct {
	Version                string                  `json:"version"`
	Mode                   string                  `json:"mode"`
	Status                 string                  `json:"status"`
	EntryFile              string                  `json:"entry_file"`
	Diagnosis              string                  `json:"diagnosis,omitempty"`
	Runs                   []paperCodeRunAttempt   `json:"runs"`
	Patches                []paperCodeAppliedPatch `json:"patches"`
	FinalSourceFingerprint string                  `json:"final_source_fingerprint,omitempty"`
	RestoredOriginals      bool                    `json:"restored_originals"`
	Summary                string                  `json:"summary"`
}

type paperCodeBackup struct {
	Content []byte
	Mode    os.FileMode
}

func (a *ResearchCodingAgent) executePaperCodeTask(ctx context.Context, task *models.Task, _ map[string]interface{}) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("research coding sandbox is not configured"))
	}
	workspacePath, entryPath, entryRelative, err := paperDebugWorkspaceEntry(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}

	mode := "baseline_runtime_debug"
	if task.Type == "fix_and_rerun" {
		mode = "result_gap_debug"
	}
	report := paperCodeDebugReport{
		Version:   "research.coding.debug/v1",
		Mode:      mode,
		Status:    "running",
		EntryFile: entryRelative,
		Runs:      []paperCodeRunAttempt{},
		Patches:   []paperCodeAppliedPatch{},
	}
	backups := map[string]paperCodeBackup{}
	repairCount := 0
	diagnostic := ""

	if task.Type == "fix_and_rerun" {
		diagnostic = chooseNonEmpty(
			extractTaskInputLike(task, "comparison_report"),
			extractTaskInputLike(task, "paper_debug_report"),
			extractTaskInputLike(task, "run_metrics"),
			task.Description,
		)
		response, contextFiles, planErr := a.planPaperCodeRepair(ctx, task, workspacePath, entryPath, diagnostic, repairCount+1)
		if planErr != nil {
			return failPaperCodeDebugTask(task, workspacePath, backups, &report, planErr)
		}
		report.Diagnosis = response.Diagnosis
		if response.Status != "patched" {
			report.Status = response.Status
			report.Summary = chooseNonEmpty(response.Diagnosis, "no code patch was justified by the available evidence")
			return completePaperCodeDebugTask(task, workspacePath, entryPath, &report, extractTaskInputLike(task, "run_metrics"))
		}
		repairCount++
		applied, applyErr := applyPaperCodePatches(workspacePath, response.Patches, contextFiles, backups, repairCount)
		if applyErr != nil {
			return failPaperCodeDebugTask(task, workspacePath, backups, &report, applyErr)
		}
		report.Patches = append(report.Patches, applied...)
	}

	for runNumber := 1; runNumber <= paperDebugMaxRuns; runNumber++ {
		attempt, runErr := a.runPaperCodeOnce(ctx, runtimeSession, workspacePath, entryRelative)
		attempt.Run = runNumber
		report.Runs = append(report.Runs, attempt)
		if runErr == nil {
			status := "passed"
			if len(report.Patches) > 0 {
				status = "repaired"
			}
			report.Status = status
			report.Summary = fmt.Sprintf("paper entry completed after %d run(s) and %d bounded repair(s)", len(report.Runs), repairCount)
			metrics := chooseNonEmpty(strings.TrimSpace(attempt.stdout), strings.TrimSpace(attempt.stderr), "paper entry completed without textual output")
			if len(attempt.images) > 0 {
				task.ImageBase64 = attempt.images[0]
			}
			return completePaperCodeDebugTask(task, workspacePath, entryPath, &report, metrics)
		}

		diagnostic = chooseNonEmpty(attempt.Error, attempt.StderrPreview, attempt.StdoutPreview, runErr.Error())
		if repairCount >= paperDebugMaxRepairs {
			return failPaperCodeDebugTask(task, workspacePath, backups, &report, fmt.Errorf("paper code failed after %d run(s) and %d repair(s): %s", len(report.Runs), repairCount, diagnostic))
		}
		if a.ChatModel == nil {
			return failPaperCodeDebugTask(task, workspacePath, backups, &report, fmt.Errorf("paper code failed and the research coding model is unavailable: %s", diagnostic))
		}

		response, contextFiles, planErr := a.planPaperCodeRepair(ctx, task, workspacePath, entryPath, diagnostic, repairCount+1)
		if planErr != nil {
			return failPaperCodeDebugTask(task, workspacePath, backups, &report, planErr)
		}
		report.Diagnosis = response.Diagnosis
		if response.Status != "patched" {
			return failPaperCodeDebugTask(task, workspacePath, backups, &report, fmt.Errorf("paper code repair stopped with status %s: %s", response.Status, response.Diagnosis))
		}
		repairCount++
		applied, applyErr := applyPaperCodePatches(workspacePath, response.Patches, contextFiles, backups, repairCount)
		if applyErr != nil {
			return failPaperCodeDebugTask(task, workspacePath, backups, &report, applyErr)
		}
		report.Patches = append(report.Patches, applied...)
		logToContext(ctx, "[%s] applied %d bounded paper-code patch(es), rerunning", a.Name, len(applied))
	}

	return failPaperCodeDebugTask(task, workspacePath, backups, &report, fmt.Errorf("paper code debug run budget exhausted"))
}

func (a *ResearchCodingAgent) runPaperCodeOnce(ctx context.Context, runtimeSession, workspacePath, entryRelative string) (paperCodeRunAttempt, error) {
	attempt := paperCodeRunAttempt{ExitCode: -1}
	before, err := paperDebugSourceFingerprint(workspacePath)
	if err != nil {
		attempt.Error = err.Error()
		return attempt, err
	}
	attempt.SourceFingerprintBefore = before

	command := fmt.Sprintf("cd /workspace && PYTHONPATH=/workspace:${PYTHONPATH:-} python3 %s", shellEscape(filepath.ToSlash(entryRelative)))
	response, runErr := a.Sandbox.ExecCommandStream(ctx, runtimeSession, []string{"bash", "-lc", command}, func(stream, line string) {
		logToContext(ctx, "[%s] paper debug %s: %s", a.Name, stream, line)
	})
	after, fingerprintErr := paperDebugSourceFingerprint(workspacePath)
	if fingerprintErr != nil {
		attempt.Error = fingerprintErr.Error()
		return attempt, fingerprintErr
	}
	attempt.SourceFingerprintAfter = after
	if before != after {
		err := fmt.Errorf("paper repository source changed during execution outside the approved patch step")
		attempt.Error = err.Error()
		return attempt, err
	}
	if runErr != nil {
		attempt.Error = runErr.Error()
		return attempt, runErr
	}
	if response == nil {
		err := fmt.Errorf("paper code execution returned nil response")
		attempt.Error = err.Error()
		return attempt, err
	}

	attempt.ExitCode = response.ExitCode
	attempt.stdout = response.Stdout
	attempt.stderr = response.Stderr
	attempt.images = append([]string(nil), response.Images...)
	attempt.StdoutPreview = truncatePaperDebugText(strings.TrimSpace(response.Stdout), paperDebugPreviewBytes)
	attempt.StderrPreview = truncatePaperDebugText(strings.TrimSpace(response.Stderr), paperDebugPreviewBytes)
	if response.ExitCode != 0 {
		err := fmt.Errorf("paper entry exited with code %d: %s", response.ExitCode, chooseNonEmpty(attempt.StderrPreview, attempt.StdoutPreview))
		attempt.Error = err.Error()
		return attempt, err
	}
	return attempt, nil
}

func (a *ResearchCodingAgent) planPaperCodeRepair(ctx context.Context, task *models.Task, workspacePath, entryPath, diagnostic string, repairNumber int) (paperCodeRepairResponse, map[string]string, error) {
	var response paperCodeRepairResponse
	if a == nil || a.ChatModel == nil {
		return response, nil, fmt.Errorf("research coding model is not configured")
	}
	contextText, contextFiles, err := collectPaperDebugContext(workspacePath, entryPath, diagnostic, extractTaskInputLike(task, "repo_manifest"))
	if err != nil {
		return response, nil, err
	}
	message, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.ResearchCodingDebugSystemPrompt},
		{Role: schema.User, Content: prompts.ResearchCodingDebugUserPrompt(task.Description, diagnostic, contextText, repairNumber)},
	})
	if err != nil {
		return response, nil, fmt.Errorf("paper code repair planning failed: %w", err)
	}
	if err := json.Unmarshal([]byte(cleanJSONResponse(message.Content)), &response); err != nil {
		return response, nil, fmt.Errorf("decode paper code repair response: %w", err)
	}
	response.Status = strings.ToLower(strings.TrimSpace(response.Status))
	response.Diagnosis = strings.TrimSpace(response.Diagnosis)
	switch response.Status {
	case "patched":
		if len(response.Patches) == 0 || len(response.Patches) > paperDebugMaxPatchFiles {
			return response, nil, fmt.Errorf("paper code repair must contain 1-%d patches", paperDebugMaxPatchFiles)
		}
		if response.Diagnosis == "" {
			return response, nil, fmt.Errorf("paper code repair must include an evidence-based diagnosis")
		}
		for _, patch := range response.Patches {
			if strings.TrimSpace(patch.Reason) == "" {
				return response, nil, fmt.Errorf("paper code patch %q must include a reason", patch.Path)
			}
		}
	case "no_change", "unsupported":
		response.Patches = nil
	default:
		return response, nil, fmt.Errorf("invalid paper code repair status %q", response.Status)
	}
	return response, contextFiles, nil
}

func collectPaperDebugContext(workspacePath, entryPath, diagnostic, repoManifest string) (string, map[string]string, error) {
	candidates := []string{entryPath}
	for _, match := range paperDebugTracebackFileRE.FindAllStringSubmatch(diagnostic, -1) {
		if len(match) > 1 {
			candidates = append(candidates, match[1])
		}
	}
	var manifest struct {
		CodeFileCandidates []string `json:"code_file_candidates"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(repoManifest)), &manifest) == nil {
		candidates = append(candidates, manifest.CodeFileCandidates...)
	}
	if entryDirectory := filepath.Dir(entryPath); entryDirectory != "" {
		entries, _ := os.ReadDir(entryDirectory)
		for _, entry := range entries {
			if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".py") {
				candidates = append(candidates, filepath.Join(entryDirectory, entry.Name()))
			}
		}
	}

	files := map[string]string{}
	ordered := make([]string, 0, paperDebugMaxContextFiles)
	totalBytes := 0
	for _, candidate := range candidates {
		relative, path, err := paperDebugExistingPythonPath(workspacePath, candidate)
		if err != nil {
			continue
		}
		if _, exists := files[relative]; exists {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) > paperDebugMaxFileBytes || totalBytes+len(raw) > paperDebugMaxContextBytes {
			continue
		}
		files[relative] = string(raw)
		ordered = append(ordered, relative)
		totalBytes += len(raw)
		if len(ordered) >= paperDebugMaxContextFiles {
			break
		}
	}
	if len(files) == 0 {
		return "", nil, fmt.Errorf("no bounded Python source context is available for paper debugging")
	}

	var builder strings.Builder
	for _, relative := range ordered {
		builder.WriteString("FILE: ")
		builder.WriteString(filepath.ToSlash(relative))
		builder.WriteString("\n")
		builder.WriteString(files[relative])
		builder.WriteString("\nEND FILE\n\n")
	}
	return builder.String(), files, nil
}

func paperDebugWorkspaceEntry(task *models.Task) (string, string, string, error) {
	workspacePath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))
	entryCandidate := chooseNonEmpty(extractTaskInputLike(task, "code_file_path"), extractTaskInputLike(task, "validated_code_file_path"))
	if workspacePath == "" || entryCandidate == "" {
		return "", "", "", fmt.Errorf("paper code debugging requires workspace_path and code_file_path")
	}
	workspaceInfo, err := os.Lstat(workspacePath)
	if err != nil || !workspaceInfo.IsDir() || workspaceInfo.Mode()&os.ModeSymlink != 0 {
		return "", "", "", fmt.Errorf("paper code workspace is not a regular directory")
	}
	relative, path, err := paperDebugExistingPythonPath(workspacePath, entryCandidate)
	if err != nil {
		return "", "", "", err
	}
	return filepath.Clean(workspacePath), path, relative, nil
}

func paperDebugExistingPythonPath(workspacePath, candidate string) (string, string, error) {
	workspacePath = filepath.Clean(workspacePath)
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return "", "", fmt.Errorf("empty paper source path")
	}
	if strings.HasPrefix(filepath.ToSlash(candidate), "/workspace/") {
		candidate = strings.TrimPrefix(filepath.ToSlash(candidate), "/workspace/")
	}
	var relative string
	if filepath.IsAbs(candidate) {
		var err error
		relative, err = filepath.Rel(workspacePath, filepath.Clean(candidate))
		if err != nil {
			return "", "", err
		}
	} else {
		relative = filepath.Clean(filepath.FromSlash(candidate))
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("paper source path escapes workspace: %s", candidate)
	}
	relativeSlash := filepath.ToSlash(relative)
	if strings.HasPrefix(relativeSlash, ".git/") || strings.HasPrefix(relativeSlash, ".scholar/") || !strings.EqualFold(filepath.Ext(relative), ".py") {
		return "", "", fmt.Errorf("paper source path is outside the allowed Python source set: %s", relativeSlash)
	}

	current := workspacePath
	parts := strings.Split(relativeSlash, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("paper source path contains a symbolic link: %s", relativeSlash)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", "", fmt.Errorf("paper source parent is not a directory: %s", relativeSlash)
		}
		if index == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() > paperDebugMaxFileBytes) {
			return "", "", fmt.Errorf("paper source must be a regular Python file no larger than %d bytes", paperDebugMaxFileBytes)
		}
	}
	return relativeSlash, current, nil
}

func applyPaperCodePatches(workspacePath string, proposals []paperCodePatchProposal, allowedFiles map[string]string, backups map[string]paperCodeBackup, repairNumber int) ([]paperCodeAppliedPatch, error) {
	seen := map[string]struct{}{}
	type validatedPatch struct {
		proposal paperCodePatchProposal
		path     string
		relative string
		original string
		mode     os.FileMode
	}
	validated := make([]validatedPatch, 0, len(proposals))
	for _, proposal := range proposals {
		relative, path, err := paperDebugExistingPythonPath(workspacePath, proposal.Path)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("duplicate paper code patch path %s", relative)
		}
		seen[relative] = struct{}{}
		allowed, ok := allowedFiles[relative]
		if !ok {
			return nil, fmt.Errorf("paper code patch targets a file outside the bounded model context: %s", relative)
		}
		content := strings.TrimSpace(proposal.Content)
		if content == "" || len(content) > paperDebugMaxFileBytes {
			return nil, fmt.Errorf("paper code patch for %s is empty or too large", relative)
		}
		if err := validatePaperCodePatchPolicy(allowed, content); err != nil {
			return nil, fmt.Errorf("paper code patch for %s violates policy: %w", relative, err)
		}
		if strings.TrimSpace(allowed) == content {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		validated = append(validated, validatedPatch{proposal: proposal, path: path, relative: relative, original: allowed, mode: info.Mode()})
	}
	if len(validated) == 0 {
		return nil, fmt.Errorf("paper code repair produced no effective patch")
	}

	applied := make([]paperCodeAppliedPatch, 0, len(validated))
	for _, patch := range validated {
		if _, exists := backups[patch.relative]; !exists {
			backups[patch.relative] = paperCodeBackup{Content: []byte(patch.original), Mode: patch.mode}
		}
		if err := writePaperDebugFile(patch.path, []byte(strings.TrimSpace(patch.proposal.Content)+"\n"), patch.mode); err != nil {
			_ = restorePaperCodeBackups(workspacePath, backups)
			return nil, err
		}
		applied = append(applied, paperCodeAppliedPatch{
			Repair: repairNumber, Path: patch.relative, Reason: strings.TrimSpace(patch.proposal.Reason),
			BeforeSHA256: paperDebugTextSHA(patch.original), AfterSHA256: paperDebugTextSHA(strings.TrimSpace(patch.proposal.Content) + "\n"),
		})
	}
	return applied, nil
}

func validatePaperCodePatchPolicy(original, patched string) error {
	lowerOriginal := strings.ToLower(original)
	lowerPatched := strings.ToLower(patched)
	for _, forbidden := range []string{
		"pip install", "fake metric", "dummy metric", "mock metric", "fabricated metric", "hardcoded metric",
		"random prediction", "dummy prediction", "hardcoded prediction", "mockllm", "fakeembedding", "fakemodel",
		"os.system(", "os.popen(", "shell=true", "shell = true", "requests.", "httpx.", "aiohttp.",
		"urllib.request", "urllib3.", "socket.socket(", "torch.hub.", "ssl._create_unverified_context",
		"verify=false", "verify = false",
	} {
		if strings.Contains(lowerPatched, forbidden) && !strings.Contains(lowerOriginal, forbidden) {
			return fmt.Errorf("introduced forbidden construct %q", forbidden)
		}
	}
	if strings.Contains(lowerPatched, "subprocess.") && !strings.Contains(lowerOriginal, "subprocess.") {
		return fmt.Errorf("introduced subprocess execution")
	}
	return nil
}

func completePaperCodeDebugTask(task *models.Task, workspacePath, entryPath string, report *paperCodeDebugReport, metrics string) error {
	fingerprint, err := paperDebugSourceFingerprint(workspacePath)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	report.FinalSourceFingerprint = fingerprint
	reportJSON, _ := json.Marshal(report)
	patchJSON, _ := json.Marshal(report.Patches)
	finalCode, _ := os.ReadFile(entryPath)
	task.Code = string(finalCode)
	task.Status = models.StatusCompleted

	if task.Type == "fix_and_rerun" {
		task.Result = string(reportJSON)
		setResearchCodingArtifacts(task, map[string]string{
			"rerun_metrics":      metrics,
			"rerun_report":       string(reportJSON),
			"gap_debug_report":   string(reportJSON),
			"gap_patch_manifest": string(patchJSON),
		})
		return nil
	}

	task.Result = metrics
	setResearchCodingArtifacts(task, map[string]string{
		"run_metrics":          metrics,
		"paper_debug_report":   string(reportJSON),
		"paper_patch_manifest": string(patchJSON),
	})
	return nil
}

func failPaperCodeDebugTask(task *models.Task, workspacePath string, backups map[string]paperCodeBackup, report *paperCodeDebugReport, failure error) error {
	if len(backups) > 0 {
		if err := restorePaperCodeBackups(workspacePath, backups); err != nil {
			failure = fmt.Errorf("%w; restore paper source failed: %v", failure, err)
		} else {
			report.RestoredOriginals = true
		}
	}
	report.Status = "failed"
	report.Summary = failure.Error()
	if fingerprint, err := paperDebugSourceFingerprint(workspacePath); err == nil {
		report.FinalSourceFingerprint = fingerprint
	}
	reportJSON, _ := json.Marshal(report)
	if task != nil {
		task.Status = models.StatusFailed
		task.Error = failure.Error()
		task.Result = string(reportJSON)
	}
	return failure
}

func restorePaperCodeBackups(workspacePath string, backups map[string]paperCodeBackup) error {
	paths := make([]string, 0, len(backups))
	for relative := range backups {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		_, path, err := paperDebugExistingPythonPath(workspacePath, relative)
		if err != nil {
			return err
		}
		backup := backups[relative]
		if err := writePaperDebugFile(path, backup.Content, backup.Mode); err != nil {
			return err
		}
	}
	return nil
}

func writePaperDebugFile(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".scholar-debug-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func paperDebugSourceFingerprint(workspacePath string) (string, error) {
	type sourceFile struct {
		path     string
		relative string
	}
	files := make([]sourceFile, 0, 128)
	err := filepath.WalkDir(workspacePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return err
		}
		relativeSlash := filepath.ToSlash(relative)
		if entry.IsDir() {
			name := strings.ToLower(entry.Name())
			if name == ".git" || name == ".scholar" || name == "__pycache__" || name == ".venv" || name == "venv" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".py", ".pyi", ".sh":
			files = append(files, sourceFile{path: path, relative: relativeSlash})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	hasher := sha256.New()
	for _, file := range files {
		raw, err := os.ReadFile(file.path)
		if err != nil {
			return "", err
		}
		hasher.Write([]byte(file.relative))
		hasher.Write([]byte{0})
		hasher.Write(raw)
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func paperDebugTextSHA(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func truncatePaperDebugText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
