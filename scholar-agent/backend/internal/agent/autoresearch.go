package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

const (
	autoResearchDefaultTrials         = 3
	autoResearchMaxTrials             = 8
	autoResearchDefaultWallSeconds    = 900
	autoResearchMaxWallSeconds        = 3600
	autoResearchDefaultValidationRuns = 1
	autoResearchMaxValidationRuns     = 5
	autoResearchMaxEditableFiles      = 8
	autoResearchMaxProtectedFiles     = 64
	autoResearchMaxPatchFiles         = 3
	autoResearchMaxFileBytes          = 96 * 1024
	autoResearchMaxProtectedBytes     = 16 * 1024 * 1024
	autoResearchOutputPreviewBytes    = 5000
	autoResearchMaxImmutableFiles     = 10000
	autoResearchMaxImmutableBytes     = 256 * 1024 * 1024
)

var (
	autoResearchMetricKeyRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	autoResearchDependencyRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(?:\[[A-Za-z0-9_,.-]+\])?(?:(?:==|~=|>=|<=|>|<)[A-Za-z0-9.*+!_-]+)?$`)
	errAutoResearchIntegrity = errors.New("AutoResearch workspace integrity failure")
)

type autoResearchCandidatePatch struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

type autoResearchCandidateResponse struct {
	Status     string                       `json:"status"`
	Hypothesis string                       `json:"hypothesis"`
	Reason     string                       `json:"reason"`
	Patches    []autoResearchCandidatePatch `json:"patches"`
}

type autoResearchFileSnapshot struct {
	Workspace string
	Relative  string
	Path      string
	Content   []byte
	Mode      os.FileMode
}

type autoResearchEvaluation struct {
	Guards []models.ResearchCommandResult
	Eval   models.ResearchCommandResult
	Metric float64
}

type autoResearchSourceFile struct {
	path     string
	relative string
	size     int64
}

func (a *ResearchCodingAgent) executeAutoResearchSpec(ctx context.Context, task *models.Task) error {
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	raw, source, err := locateAutoResearchSpec(task, workspacePath)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, err := decodeAutoResearchSpec(raw)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := normalizeAndFreezeAutoResearchSpec(task, workspacePath, source, raw, &spec); err != nil {
		return failResearchCodingTask(task, err)
	}

	specJSON, _ := json.Marshal(spec)
	dependenciesJSON, _ := json.Marshal(spec.Dependencies)
	specHash := autoResearchSHA(specJSON)
	report := fmt.Sprintf("frozen AutoResearch spec %s (%s): %d editable file(s), %d protected file(s), max %d trial(s), %ds wall time, %d validation run(s)", spec.Name, specHash[:12], len(spec.EditableFiles), len(spec.ProtectedFiles), spec.MaxTrials, spec.MaxWallSeconds, spec.ValidationRuns)

	task.Result = report
	task.StructuredData = string(specJSON)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"research_spec":        string(specJSON),
		"research_spec_report": report,
		"dependency_spec":      string(dependenciesJSON),
	})
	logToContext(ctx, "[%s] %s", a.Name, report)
	return nil
}

func locateAutoResearchSpec(task *models.Task, workspacePath string) ([]byte, string, error) {
	for _, key := range []string{"research_spec", "autoresearch_spec"} {
		if task != nil && task.Inputs != nil && task.Inputs[key] != nil {
			value := task.Inputs[key]
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return []byte(text), "task_input:" + key, nil
			}
			raw, err := json.Marshal(value)
			if err == nil && len(raw) > 0 && string(raw) != "null" {
				return raw, "task_input:" + key, nil
			}
		}
	}

	candidates := make([]string, 0, 8)
	for _, key := range []string{"research_spec_path", "autoresearch_spec_path"} {
		if value := strings.TrimSpace(extractTaskInputLike(task, key)); value != "" {
			candidates = append(candidates, value)
		}
	}
	candidates = append(candidates, ".scholar/autoresearch/spec.json", "autoresearch.json")
	uploadDirectory := filepath.Join(workspacePath, ".scholar", "uploads")
	if entries, err := os.ReadDir(uploadDirectory); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				candidates = append(candidates, filepath.ToSlash(filepath.Join(".scholar", "uploads", entry.Name())))
			}
		}
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		relative, path, err := autoResearchExistingFile(workspacePath, candidate, autoResearchMaxProtectedBytes, true)
		if err != nil {
			continue
		}
		if _, duplicate := seen[relative]; duplicate {
			continue
		}
		seen[relative] = struct{}{}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var header struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &header) == nil && header.Version == models.AutoResearchSpecVersion {
			return raw, relative, nil
		}
	}
	return nil, "", fmt.Errorf("AutoResearch spec not found; add autoresearch.json to the repository or upload an %s JSON file", models.AutoResearchSpecVersion)
}

func decodeAutoResearchSpec(raw []byte) (models.ResearchSpec, error) {
	var spec models.ResearchSpec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return spec, fmt.Errorf("decode AutoResearch spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return spec, fmt.Errorf("decode AutoResearch spec: multiple JSON values are not allowed")
	}
	return spec, nil
}

func normalizeAndFreezeAutoResearchSpec(task *models.Task, workspacePath, source string, raw []byte, spec *models.ResearchSpec) error {
	if spec == nil {
		return fmt.Errorf("AutoResearch spec is nil")
	}
	if spec.Version != models.AutoResearchSpecVersion {
		return fmt.Errorf("unsupported AutoResearch spec version %q", spec.Version)
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Objective = strings.TrimSpace(spec.Objective)
	if spec.Name == "" || spec.Objective == "" {
		return fmt.Errorf("AutoResearch spec requires name and objective")
	}
	if len(spec.Name) > 120 || len(spec.Objective) > 2000 {
		return fmt.Errorf("AutoResearch spec name or objective is too long")
	}

	direction := strings.ToLower(strings.TrimSpace(spec.Direction))
	if direction == "" {
		direction = "maximize"
	}
	if direction != "maximize" && direction != "minimize" {
		return fmt.Errorf("AutoResearch direction must be maximize or minimize")
	}
	spec.Direction = direction
	spec.MetricKey = strings.TrimSpace(spec.MetricKey)
	if !autoResearchMetricKeyRE.MatchString(spec.MetricKey) {
		return fmt.Errorf("AutoResearch metric_key is invalid")
	}
	if math.IsNaN(spec.MinDelta) || math.IsInf(spec.MinDelta, 0) || spec.MinDelta < 0 {
		return fmt.Errorf("AutoResearch min_delta must be a finite non-negative number")
	}

	inputMaxTrials := boundedAutoResearchInput(task, "autoresearch_max_trials", autoResearchDefaultTrials, 1, autoResearchMaxTrials)
	if spec.MaxTrials <= 0 {
		spec.MaxTrials = inputMaxTrials
	} else if spec.MaxTrials > inputMaxTrials {
		spec.MaxTrials = inputMaxTrials
	}
	if spec.MaxTrials > autoResearchMaxTrials {
		spec.MaxTrials = autoResearchMaxTrials
	}
	inputWallSeconds := boundedAutoResearchInput(task, "autoresearch_max_wall_seconds", autoResearchDefaultWallSeconds, 1, autoResearchMaxWallSeconds)
	if spec.MaxWallSeconds <= 0 {
		spec.MaxWallSeconds = inputWallSeconds
	} else if spec.MaxWallSeconds > inputWallSeconds {
		spec.MaxWallSeconds = inputWallSeconds
	}
	if spec.MaxWallSeconds > autoResearchMaxWallSeconds {
		spec.MaxWallSeconds = autoResearchMaxWallSeconds
	}
	requestedValidationRuns, hasValidationRunLimit := optionalBoundedAutoResearchInput(task, "autoresearch_validation_runs", 1, autoResearchMaxValidationRuns)
	if spec.ValidationRuns <= 0 {
		spec.ValidationRuns = autoResearchDefaultValidationRuns
		if hasValidationRunLimit {
			spec.ValidationRuns = requestedValidationRuns
		}
	} else if hasValidationRunLimit && spec.ValidationRuns > requestedValidationRuns {
		spec.ValidationRuns = requestedValidationRuns
	}
	if spec.ValidationRuns > autoResearchMaxValidationRuns {
		spec.ValidationRuns = autoResearchMaxValidationRuns
	}

	if err := validateAutoResearchCommand(spec.EvalCommand); err != nil {
		return fmt.Errorf("invalid eval_command: %w", err)
	}
	if len(spec.GuardCommands) > 6 {
		return fmt.Errorf("AutoResearch supports at most 6 guard commands")
	}
	for index, command := range spec.GuardCommands {
		if err := validateAutoResearchCommand(command); err != nil {
			return fmt.Errorf("invalid guard_commands[%d]: %w", index, err)
		}
	}
	for _, dependency := range spec.Dependencies {
		if !autoResearchDependencyRE.MatchString(strings.TrimSpace(dependency)) {
			return fmt.Errorf("unsafe or unsupported dependency %q", dependency)
		}
	}
	if len(spec.Dependencies) > 32 {
		return fmt.Errorf("AutoResearch supports at most 32 dependencies")
	}
	spec.Dependencies = cleanAutoResearchStrings(spec.Dependencies)

	editable, err := normalizeAutoResearchPaths(workspacePath, spec.EditableFiles, autoResearchMaxEditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return fmt.Errorf("editable_files: %w", err)
	}
	if len(editable) == 0 {
		return fmt.Errorf("AutoResearch requires at least one editable file")
	}
	protectedInputs := append([]string(nil), spec.ProtectedFiles...)
	if source != "" && !strings.HasPrefix(source, "task_input:") {
		protectedInputs = append(protectedInputs, source)
	}
	protected, err := normalizeAutoResearchPaths(workspacePath, protectedInputs, autoResearchMaxProtectedFiles, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return fmt.Errorf("protected_files: %w", err)
	}
	if len(protected) == 0 {
		return fmt.Errorf("AutoResearch requires at least one protected evaluator or data file")
	}
	editableSet := make(map[string]struct{}, len(editable))
	for _, relative := range editable {
		editableSet[relative] = struct{}{}
	}
	for _, relative := range protected {
		if _, overlap := editableSet[relative]; overlap {
			return fmt.Errorf("file %s cannot be both editable and protected", relative)
		}
	}
	if err := validateAutoResearchEvaluatorScope(workspacePath, spec.EvalCommand, editableSet); err != nil {
		return err
	}

	spec.EditableFiles = editable
	spec.ProtectedFiles = protected
	spec.FrozenProtected, err = hashAutoResearchFiles(workspacePath, protected, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return err
	}
	spec.FrozenWorkspace, err = autoResearchImmutableFingerprint(workspacePath, editable)
	if err != nil {
		return err
	}
	spec.Source = source
	spec.SourceSHA256 = autoResearchSHA(raw)
	spec.CreatedAt = time.Now().UTC()
	return nil
}

func (a *ResearchCodingAgent) executeAutoResearchRun(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch sandbox is not configured"))
	}
	if a.ChatModel == nil {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch candidate model is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, specJSON, specHash, err := autoResearchSpecFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateFrozenAutoResearchSpec(spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateAutoResearchEvaluatorScope(workspacePath, spec.EvalCommand, autoResearchPathSet(spec.EditableFiles)); err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := verifyAutoResearchSpecFiles(workspacePath, spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); err != nil {
		return failResearchCodingTask(task, err)
	}

	editableSnapshots, err := snapshotAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	protectedSnapshots, err := snapshotAutoResearchFiles(workspacePath, spec.ProtectedFiles, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	immutableSnapshots, err := snapshotAutoResearchImmutableFiles(workspacePath, spec.EditableFiles)
	if err != nil {
		return failResearchCodingTask(task, err)
	}

	startedAt := time.Now().UTC()
	ledger := models.ResearchTrialLedger{
		Version: models.AutoResearchLedgerVersion, SpecSHA256: specHash, Status: "running",
		MetricKey: spec.MetricKey, Direction: spec.Direction, MaxTrials: spec.MaxTrials,
		ProtectedFiles: append([]models.ResearchFileHash(nil), spec.FrozenProtected...),
		Trials:         []models.ResearchTrial{}, StartedAt: startedAt,
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.MaxWallSeconds)*time.Second)
	defer cancel()

	baselineEditableHashes, err := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return failAutoResearchRun(task, &ledger, err)
	}
	baselineStart := time.Now().UTC()
	baseline, baselineErr := a.evaluateAutoResearchCandidate(runCtx, runtimeSession, workspacePath, spec)
	if baselineErr == nil {
		currentHashes, hashErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
		if hashErr != nil {
			baselineErr = hashErr
		} else if !equalAutoResearchHashes(currentHashes, baselineEditableHashes) {
			baselineErr = fmt.Errorf("editable files changed during frozen baseline evaluation")
		}
	}
	baselineTrial := models.ResearchTrial{
		Number: 0, Status: "baseline", Hypothesis: "frozen baseline", Decision: "keep",
		Reason: "baseline establishes the initial score", GuardResults: baseline.Guards,
		EvalResult: baseline.Eval, StartedAt: baselineStart, FinishedAt: time.Now().UTC(),
	}
	if baselineErr != nil {
		baselineTrial.Status = "failed"
		baselineTrial.Decision = "abort"
		baselineTrial.Reason = baselineErr.Error()
		ledger.Trials = append(ledger.Trials, baselineTrial)
		ledger.Status = "failed"
		ledger.StopReason = "baseline_failed"
		ledger.FinishedAt = time.Now().UTC()
		if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, editableSnapshots); restoreErr != nil {
			baselineErr = fmt.Errorf("%w; baseline restore failed: %v", baselineErr, restoreErr)
			baselineTrial.Reason = baselineErr.Error()
			ledger.Trials[len(ledger.Trials)-1].Reason = baselineTrial.Reason
		}
		return failAutoResearchRun(task, &ledger, baselineErr)
	}
	baselineMetric := baseline.Metric
	baselineTrial.Metric = &baselineMetric
	ledger.Trials = append(ledger.Trials, baselineTrial)
	ledger.BaselineScore = baselineMetric
	ledger.BestScore = baselineMetric
	bestSnapshots := editableSnapshots
	logToContext(ctx, "[%s] AutoResearch baseline %s=%.8g", a.Name, spec.MetricKey, baselineMetric)

	for trialNumber := 1; trialNumber <= spec.MaxTrials; trialNumber++ {
		if runCtx.Err() != nil {
			ledger.StopReason = "wall_time_budget_exhausted"
			break
		}
		proposal, proposalErr := a.proposeAutoResearchCandidate(runCtx, spec, specJSON, ledger, bestSnapshots)
		if proposalErr != nil {
			ledger.StopReason = "candidate_model_error: " + proposalErr.Error()
			break
		}
		if proposal.Status != "propose" {
			ledger.StopReason = proposal.Status + ": " + chooseNonEmpty(proposal.Reason, "candidate model stopped")
			break
		}

		trial := models.ResearchTrial{
			Number: trialNumber, Status: "running", Hypothesis: proposal.Hypothesis,
			Decision: "reject", Reason: proposal.Reason, StartedAt: time.Now().UTC(),
		}
		patches, applyErr := applyAutoResearchCandidate(workspacePath, proposal, bestSnapshots)
		trial.Patches = patches
		if applyErr != nil {
			trial.Status = "rejected"
			trial.Reason = "candidate rejected before execution: " + applyErr.Error()
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
				ledger.Status = "failed"
				ledger.StopReason = "candidate_restore_failed"
				return failAutoResearchRun(task, &ledger, fmt.Errorf("candidate rejection restore failed: %w", restoreErr))
			}
			continue
		}
		expectedCandidate, hashErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
		if hashErr != nil {
			trial.Status = "rejected"
			trial.Reason = "candidate hash failed: " + hashErr.Error()
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
				ledger.Status = "failed"
				ledger.StopReason = "candidate_restore_failed"
				return failAutoResearchRun(task, &ledger, fmt.Errorf("candidate hash restore failed: %w", restoreErr))
			}
			continue
		}

		evaluation, evalErr := a.evaluateAutoResearchCandidate(runCtx, runtimeSession, workspacePath, spec)
		trial.GuardResults = evaluation.Guards
		trial.EvalResult = evaluation.Eval
		if integrityErr := verifyAutoResearchSpecFiles(workspacePath, spec); integrityErr != nil {
			trial.Status = "compromised"
			trial.Decision = "abort"
			trial.Reason = integrityErr.Error()
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			ledger.Status = "compromised"
			ledger.StopReason = "protected_file_integrity_failure"
			ledger.FinishedAt = time.Now().UTC()
			if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, bestSnapshots); restoreErr != nil {
				integrityErr = fmt.Errorf("%w; compromised workspace restore failed: %v", integrityErr, restoreErr)
			}
			return failAutoResearchRun(task, &ledger, integrityErr)
		}
		if evalErr != nil && errors.Is(evalErr, errAutoResearchIntegrity) {
			trial.Status = "compromised"
			trial.Decision = "abort"
			trial.Reason = evalErr.Error()
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			ledger.Status = "compromised"
			ledger.StopReason = "workspace_integrity_failure"
			ledger.FinishedAt = time.Now().UTC()
			if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, bestSnapshots); restoreErr != nil {
				evalErr = fmt.Errorf("%w; compromised workspace restore failed: %v", evalErr, restoreErr)
			}
			return failAutoResearchRun(task, &ledger, evalErr)
		}
		if evalErr == nil {
			if current, hashErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false); hashErr != nil || !equalAutoResearchHashes(current, expectedCandidate) {
				evalErr = fmt.Errorf("editable files changed during frozen evaluation")
			}
		}

		if evalErr != nil {
			trial.Status = "rejected"
			trial.Reason = "candidate execution failed: " + evalErr.Error()
			if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
				trial.Status = "failed"
				trial.Decision = "abort"
				trial.Reason += "; restore failed: " + restoreErr.Error()
				trial.FinishedAt = time.Now().UTC()
				ledger.Trials = append(ledger.Trials, trial)
				ledger.CompletedTrials++
				ledger.Status = "failed"
				ledger.StopReason = "candidate_restore_failed"
				return failAutoResearchRun(task, &ledger, restoreErr)
			}
		} else {
			metric := evaluation.Metric
			delta := autoResearchDelta(metric, ledger.BestScore, spec.Direction)
			trial.Metric = &metric
			trial.DeltaFromBest = &delta
			if autoResearchImproved(metric, ledger.BestScore, spec.Direction, spec.MinDelta) {
				trial.Status = "kept"
				trial.Decision = "keep"
				trial.Reason = fmt.Sprintf("%s improved by %.8g (required %.8g)", spec.MetricKey, delta, spec.MinDelta)
				ledger.BestScore = metric
				ledger.AcceptedTrials++
				newBestSnapshots, snapshotErr := snapshotAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
				if snapshotErr != nil {
					return failAutoResearchRun(task, &ledger, snapshotErr)
				}
				bestSnapshots = newBestSnapshots
			} else {
				trial.Status = "rejected"
				trial.Reason = fmt.Sprintf("%s delta %.8g did not meet %.8g", spec.MetricKey, delta, spec.MinDelta)
				if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
					trial.Status = "failed"
					trial.Decision = "abort"
					trial.Reason += "; restore failed: " + restoreErr.Error()
					trial.FinishedAt = time.Now().UTC()
					ledger.Trials = append(ledger.Trials, trial)
					ledger.CompletedTrials++
					ledger.Status = "failed"
					ledger.StopReason = "candidate_restore_failed"
					return failAutoResearchRun(task, &ledger, restoreErr)
				}
			}
		}
		trial.FinishedAt = time.Now().UTC()
		ledger.Trials = append(ledger.Trials, trial)
		ledger.CompletedTrials++
		logToContext(ctx, "[%s] AutoResearch trial %d: %s", a.Name, trialNumber, trial.Status)
	}

	if ledger.StopReason == "" {
		ledger.StopReason = "trial_budget_exhausted"
	}
	ledger.Status = "completed"
	ledger.FinishedAt = time.Now().UTC()
	finalizeAutoResearchLedgerUsage(&ledger)
	bestHashes, err := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return failAutoResearchRun(task, &ledger, err)
	}
	ledger.BestCandidateFiles = bestHashes
	best := models.ResearchBestCandidate{
		SpecSHA256: specHash, Score: ledger.BestScore, MetricKey: spec.MetricKey,
		Direction: spec.Direction, AcceptedTrials: ledger.AcceptedTrials, Files: bestHashes,
	}
	ledgerJSON, _ := json.Marshal(ledger)
	bestJSON, _ := json.Marshal(best)
	report := fmt.Sprintf("AutoResearch completed %d/%d candidate trial(s); kept %d; %s %.8g -> %.8g; commands=%d; command_time=%dms; stop=%s", ledger.CompletedTrials, spec.MaxTrials, ledger.AcceptedTrials, spec.MetricKey, ledger.BaselineScore, ledger.BestScore, ledger.ResourceUsage.CommandRuns, ledger.ResourceUsage.CommandDurationMS, ledger.StopReason)

	task.Result = string(ledgerJSON)
	task.StructuredData = string(ledgerJSON)
	if len(bestSnapshots) > 0 {
		task.Code = string(bestSnapshots[0].Content)
	}
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"research_trial_ledger":   string(ledgerJSON),
		"research_best_candidate": string(bestJSON),
		"research_run_report":     report,
	})
	return nil
}

func (a *ResearchCodingAgent) executeAutoResearchValidation(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch sandbox is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, _, specHash, err := autoResearchSpecFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateFrozenAutoResearchSpec(spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateAutoResearchEvaluatorScope(workspacePath, spec.EvalCommand, autoResearchPathSet(spec.EditableFiles)); err != nil {
		return failResearchCodingTask(task, err)
	}
	ledger, _, ledgerHash, err := autoResearchLedgerFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if ledger.SpecSHA256 != specHash {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch ledger does not match the frozen spec"))
	}
	if err := validateAutoResearchLedgerAgainstSpec(ledger, spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	best, err := autoResearchBestCandidateFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateAutoResearchBestCandidate(best, ledger, specHash); err != nil {
		return failResearchCodingTask(task, err)
	}

	validationStartedAt := time.Now().UTC()
	requestedRuns := effectiveAutoResearchValidationRuns(spec)
	protectedErr := verifyAutoResearchSpecFiles(workspacePath, spec)
	immutableErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace)
	candidateHashes, candidateErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	protectedIntact := protectedErr == nil
	workspaceIntact := immutableErr == nil
	candidateIntact := candidateErr == nil && equalAutoResearchHashes(candidateHashes, ledger.BestCandidateFiles)
	runs := make([]models.ResearchValidationRun, 0, requestedRuns)
	observedScores := make([]float64, 0, requestedRuns)
	legacyEvaluation := autoResearchEvaluation{Guards: []models.ResearchCommandResult{}}
	legacyEvaluationSet := false
	passedRuns := 0
	var validationErr error
	if !protectedIntact {
		validationErr = protectedErr
	} else if !workspaceIntact {
		validationErr = immutableErr
	} else if !candidateIntact {
		validationErr = fmt.Errorf("best candidate files do not match the trial ledger")
	} else {
		protectedSnapshots, snapshotErr := snapshotAutoResearchFiles(workspacePath, spec.ProtectedFiles, autoResearchMaxProtectedBytes, true)
		if snapshotErr != nil {
			return failResearchCodingTask(task, snapshotErr)
		}
		candidateSnapshots, snapshotErr := snapshotAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
		if snapshotErr != nil {
			return failResearchCodingTask(task, snapshotErr)
		}
		immutableSnapshots, snapshotErr := snapshotAutoResearchImmutableFiles(workspacePath, spec.EditableFiles)
		if snapshotErr != nil {
			return failResearchCodingTask(task, snapshotErr)
		}
		tolerance := math.Max(1e-9, math.Abs(ledger.BestScore)*1e-6)
		for runNumber := 1; runNumber <= requestedRuns; runNumber++ {
			if ctx.Err() != nil {
				validationErr = ctx.Err()
				break
			}
			run := models.ResearchValidationRun{Number: runNumber, Status: "running", StartedAt: time.Now().UTC()}
			evaluation, evalErr := a.evaluateAutoResearchCandidate(ctx, runtimeSession, workspacePath, spec)
			run.GuardResults = evaluation.Guards
			run.EvalResult = evaluation.Eval
			if !legacyEvaluationSet {
				legacyEvaluation = evaluation
				legacyEvaluationSet = true
			}

			runProtectedErr := verifyAutoResearchSpecFiles(workspacePath, spec)
			runImmutableErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace)
			candidateHashes, candidateErr = hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
			runProtectedIntact := runProtectedErr == nil
			runWorkspaceIntact := runImmutableErr == nil
			runCandidateIntact := candidateErr == nil && equalAutoResearchHashes(candidateHashes, ledger.BestCandidateFiles)
			protectedIntact = protectedIntact && runProtectedIntact
			workspaceIntact = workspaceIntact && runWorkspaceIntact
			candidateIntact = candidateIntact && runCandidateIntact
			integrityFailure := !runProtectedIntact || !runWorkspaceIntact || !runCandidateIntact
			if integrityFailure {
				if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, candidateSnapshots); restoreErr != nil {
					evalErr = fmt.Errorf("validation changed frozen files and restore failed: %w", restoreErr)
				} else if evalErr == nil {
					switch {
					case !runProtectedIntact:
						evalErr = runProtectedErr
					case !runWorkspaceIntact:
						evalErr = runImmutableErr
					default:
						evalErr = fmt.Errorf("validation changed best candidate files")
					}
				}
			}

			if evalErr == nil {
				observed := evaluation.Metric
				run.ObservedScore = &observed
				observedScores = append(observedScores, observed)
				run.ScoreMatches = math.Abs(observed-ledger.BestScore) <= tolerance
				if run.ScoreMatches {
					run.Status = "validated"
					passedRuns++
				} else {
					run.Status = "failed"
					run.Error = fmt.Sprintf("observed %s=%.8g differs from expected %.8g", spec.MetricKey, observed, ledger.BestScore)
				}
			} else {
				run.Status = "failed"
				run.Error = evalErr.Error()
			}
			run.FinishedAt = time.Now().UTC()
			runs = append(runs, run)
			if run.Status == "failed" && validationErr == nil {
				validationErr = fmt.Errorf("validation run %d: %s", run.Number, run.Error)
			}
			if integrityFailure || ctx.Err() != nil {
				break
			}
		}
	}
	meanScore, stddev, minScore, maxScore := autoResearchScoreStats(observedScores)
	completedRuns := len(runs)
	failedRuns := completedRuns - passedRuns
	unfinishedRuns := requestedRuns - completedRuns
	failureRate := float64(failedRuns+unfinishedRuns) / float64(requestedRuns)
	scoreMatches := passedRuns == requestedRuns
	status := "validated"
	summary := fmt.Sprintf("%d independent evaluator run(s) confirmed %s=%.8g (stddev %.8g)", requestedRuns, spec.MetricKey, meanScore, stddev)
	if requestedRuns == 1 {
		summary = fmt.Sprintf("independent evaluator confirmed %s=%.8g", spec.MetricKey, meanScore)
	}
	if !protectedIntact || !workspaceIntact || !candidateIntact || !scoreMatches {
		status = "failed"
		summary = fmt.Sprintf("validation failed: passed=%d/%d failed=%d unfinished=%d protected_intact=%t workspace_intact=%t candidate_intact=%t", passedRuns, requestedRuns, failedRuns, unfinishedRuns, protectedIntact, workspaceIntact, candidateIntact)
		if validationErr != nil {
			summary += "; evaluator: " + validationErr.Error()
		}
	}
	validatedAt := time.Now().UTC()
	resourceUsage := summarizeAutoResearchValidationUsage(runs, validationStartedAt, validatedAt)
	report := models.ResearchValidationReport{
		Version: models.AutoResearchValidationVersion, Status: status, SpecSHA256: specHash,
		LedgerSHA256: ledgerHash, ExpectedScore: ledger.BestScore, ObservedScore: meanScore,
		ObservedScores: observedScores, MeanScore: meanScore, StdDev: stddev, MinScore: minScore, MaxScore: maxScore,
		MetricKey: spec.MetricKey, ScoreMatches: scoreMatches, ProtectedIntact: protectedIntact,
		RequestedRuns: requestedRuns, CompletedRuns: completedRuns, PassedRuns: passedRuns, FailedRuns: failedRuns,
		UnfinishedRuns: unfinishedRuns, FailureRate: failureRate, WorkspaceIntact: workspaceIntact,
		CandidateIntact: candidateIntact, GuardResults: legacyEvaluation.Guards, EvalResult: legacyEvaluation.Eval,
		Runs: runs, ResourceUsage: resourceUsage, ValidatedAt: validatedAt, Summary: summary,
	}
	reportJSON, _ := json.Marshal(report)
	metricsJSON, _ := json.Marshal(map[string]any{
		"metric_key": spec.MetricKey, "score": meanScore, "expected_score": ledger.BestScore,
		"stddev": stddev, "observed_scores": observedScores, "requested_runs": requestedRuns,
		"min_score": minScore, "max_score": maxScore, "completed_runs": completedRuns,
		"passed_runs": passedRuns, "failure_rate": failureRate, "status": status,
		"spec_sha256": specHash, "ledger_sha256": ledgerHash,
	})
	task.Result = string(reportJSON)
	task.StructuredData = string(reportJSON)
	setResearchCodingArtifacts(task, map[string]string{
		"research_validation_report": string(reportJSON),
		"research_best_metrics":      string(metricsJSON),
	})
	if status != "validated" {
		return failResearchCodingTask(task, fmt.Errorf("%s", summary))
	}
	task.Status = models.StatusCompleted
	logToContext(ctx, "[%s] %s", a.Name, summary)
	return nil
}

func effectiveAutoResearchValidationRuns(spec models.ResearchSpec) int {
	if spec.ValidationRuns <= 0 {
		return autoResearchDefaultValidationRuns
	}
	return spec.ValidationRuns
}

func autoResearchScoreStats(scores []float64) (mean, stddev, minimum, maximum float64) {
	if len(scores) == 0 {
		return 0, 0, 0, 0
	}
	minimum = scores[0]
	maximum = scores[0]
	for _, score := range scores {
		mean += score
		if score < minimum {
			minimum = score
		}
		if score > maximum {
			maximum = score
		}
	}
	mean /= float64(len(scores))
	for _, score := range scores {
		delta := score - mean
		stddev += delta * delta
	}
	stddev = math.Sqrt(stddev / float64(len(scores)))
	return mean, stddev, minimum, maximum
}

func summarizeAutoResearchValidationUsage(runs []models.ResearchValidationRun, startedAt, finishedAt time.Time) models.ResearchResourceUsage {
	usage := models.ResearchResourceUsage{}
	for _, run := range runs {
		for _, command := range run.GuardResults {
			addAutoResearchCommandUsage(&usage, command, true)
		}
		addAutoResearchCommandUsage(&usage, run.EvalResult, false)
	}
	usage.WallDurationMS = autoResearchElapsedMS(startedAt, finishedAt)
	return usage
}

func autoResearchSpecFromTask(task *models.Task) (models.ResearchSpec, []byte, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "research_spec"))
	if raw == "" {
		return models.ResearchSpec{}, nil, "", fmt.Errorf("research_spec input is required")
	}
	var spec models.ResearchSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return spec, nil, "", fmt.Errorf("decode frozen research_spec: %w", err)
	}
	if spec.Version != models.AutoResearchSpecVersion || len(spec.FrozenProtected) == 0 {
		return spec, nil, "", fmt.Errorf("research_spec is not frozen %s data", models.AutoResearchSpecVersion)
	}
	return spec, []byte(raw), autoResearchSHA([]byte(raw)), nil
}

func validateFrozenAutoResearchSpec(spec models.ResearchSpec) error {
	if spec.Version != models.AutoResearchSpecVersion || strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Objective) == "" {
		return fmt.Errorf("frozen AutoResearch spec identity is invalid")
	}
	if spec.Direction != "maximize" && spec.Direction != "minimize" {
		return fmt.Errorf("frozen AutoResearch direction is invalid")
	}
	if !autoResearchMetricKeyRE.MatchString(spec.MetricKey) || spec.MinDelta < 0 || math.IsNaN(spec.MinDelta) || math.IsInf(spec.MinDelta, 0) {
		return fmt.Errorf("frozen AutoResearch metric contract is invalid")
	}
	if spec.MaxTrials < 1 || spec.MaxTrials > autoResearchMaxTrials || spec.MaxWallSeconds < 1 || spec.MaxWallSeconds > autoResearchMaxWallSeconds {
		return fmt.Errorf("frozen AutoResearch budget exceeds runtime limits")
	}
	if spec.ValidationRuns < 0 || spec.ValidationRuns > autoResearchMaxValidationRuns {
		return fmt.Errorf("frozen AutoResearch validation run count exceeds runtime limits")
	}
	if len(spec.EditableFiles) == 0 || len(spec.EditableFiles) > autoResearchMaxEditableFiles || len(spec.ProtectedFiles) == 0 || len(spec.ProtectedFiles) > autoResearchMaxProtectedFiles {
		return fmt.Errorf("frozen AutoResearch file scope is invalid")
	}
	editable := make(map[string]struct{}, len(spec.EditableFiles))
	for _, path := range spec.EditableFiles {
		if path == "" {
			return fmt.Errorf("frozen AutoResearch editable path is empty")
		}
		editable[path] = struct{}{}
	}
	if len(editable) != len(spec.EditableFiles) {
		return fmt.Errorf("frozen AutoResearch editable paths contain duplicates")
	}
	if len(spec.FrozenProtected) != len(spec.ProtectedFiles) {
		return fmt.Errorf("frozen AutoResearch protected hash set is incomplete")
	}
	if len(spec.FrozenWorkspace) != sha256.Size*2 {
		return fmt.Errorf("frozen AutoResearch workspace fingerprint is invalid")
	}
	if _, err := hex.DecodeString(spec.FrozenWorkspace); err != nil {
		return fmt.Errorf("frozen AutoResearch workspace fingerprint is invalid")
	}
	protected := make(map[string]struct{}, len(spec.ProtectedFiles))
	for _, path := range spec.ProtectedFiles {
		if path == "" {
			return fmt.Errorf("frozen AutoResearch protected path is empty")
		}
		if _, overlap := editable[path]; overlap {
			return fmt.Errorf("frozen AutoResearch editable and protected paths overlap")
		}
		protected[path] = struct{}{}
	}
	if len(protected) != len(spec.ProtectedFiles) {
		return fmt.Errorf("frozen AutoResearch protected paths contain duplicates")
	}
	hashedPaths := make(map[string]struct{}, len(spec.FrozenProtected))
	for _, file := range spec.FrozenProtected {
		if _, ok := protected[file.Path]; !ok || len(file.SHA256) != sha256.Size*2 {
			return fmt.Errorf("frozen AutoResearch protected hash is invalid")
		}
		if _, duplicate := hashedPaths[file.Path]; duplicate {
			return fmt.Errorf("frozen AutoResearch protected hashes contain duplicates")
		}
		hashedPaths[file.Path] = struct{}{}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("frozen AutoResearch protected hash is invalid")
		}
	}
	if err := validateAutoResearchCommand(spec.EvalCommand); err != nil {
		return fmt.Errorf("frozen AutoResearch eval command is invalid: %w", err)
	}
	if len(spec.GuardCommands) > 6 {
		return fmt.Errorf("frozen AutoResearch guard command count is invalid")
	}
	for _, command := range spec.GuardCommands {
		if err := validateAutoResearchCommand(command); err != nil {
			return fmt.Errorf("frozen AutoResearch guard command is invalid: %w", err)
		}
	}
	if len(spec.Dependencies) > 32 {
		return fmt.Errorf("frozen AutoResearch dependency count is invalid")
	}
	for _, dependency := range spec.Dependencies {
		if !autoResearchDependencyRE.MatchString(dependency) {
			return fmt.Errorf("frozen AutoResearch dependency is invalid")
		}
	}
	return nil
}

func autoResearchLedgerFromTask(task *models.Task) (models.ResearchTrialLedger, []byte, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "research_trial_ledger"))
	if raw == "" {
		return models.ResearchTrialLedger{}, nil, "", fmt.Errorf("research_trial_ledger input is required")
	}
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(raw), &ledger); err != nil {
		return ledger, nil, "", fmt.Errorf("decode research_trial_ledger: %w", err)
	}
	if ledger.Version != models.AutoResearchLedgerVersion || ledger.Status != "completed" {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger is not a completed %s ledger", models.AutoResearchLedgerVersion)
	}
	if ledger.MaxTrials < 1 || ledger.MaxTrials > autoResearchMaxTrials || ledger.CompletedTrials < 0 || ledger.CompletedTrials > ledger.MaxTrials || ledger.AcceptedTrials < 0 || ledger.AcceptedTrials > ledger.CompletedTrials {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger contains invalid trial counts")
	}
	if len(ledger.Trials) != ledger.CompletedTrials+1 || len(ledger.BestCandidateFiles) == 0 || len(ledger.ProtectedFiles) == 0 {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger contains incomplete evidence")
	}
	if math.IsNaN(ledger.BaselineScore) || math.IsInf(ledger.BaselineScore, 0) || math.IsNaN(ledger.BestScore) || math.IsInf(ledger.BestScore, 0) {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger contains non-finite scores")
	}
	return ledger, []byte(raw), autoResearchSHA([]byte(raw)), nil
}

func validateAutoResearchLedgerAgainstSpec(ledger models.ResearchTrialLedger, spec models.ResearchSpec) error {
	if ledger.MetricKey != spec.MetricKey || ledger.Direction != spec.Direction || ledger.MaxTrials != spec.MaxTrials {
		return fmt.Errorf("research_trial_ledger metric, direction or budget does not match the frozen spec")
	}
	if !equalAutoResearchHashes(ledger.ProtectedFiles, spec.FrozenProtected) {
		return fmt.Errorf("research_trial_ledger protected files do not match the frozen spec")
	}
	if len(ledger.Trials) == 0 {
		return fmt.Errorf("research_trial_ledger has no baseline trial")
	}
	baseline := ledger.Trials[0]
	if baseline.Number != 0 || baseline.Status != "baseline" || baseline.Decision != "keep" || baseline.Metric == nil ||
		math.Float64bits(*baseline.Metric) != math.Float64bits(ledger.BaselineScore) {
		return fmt.Errorf("research_trial_ledger baseline evidence is inconsistent")
	}

	bestScore := ledger.BaselineScore
	accepted := 0
	for index, trial := range ledger.Trials[1:] {
		if trial.Number != index+1 {
			return fmt.Errorf("research_trial_ledger trial numbers are not sequential")
		}
		if trial.Metric != nil && (math.IsNaN(*trial.Metric) || math.IsInf(*trial.Metric, 0)) {
			return fmt.Errorf("research_trial_ledger trial %d contains a non-finite metric", trial.Number)
		}
		switch trial.Status {
		case "kept":
			if trial.Decision != "keep" || trial.Metric == nil || !autoResearchImproved(*trial.Metric, bestScore, spec.Direction, spec.MinDelta) {
				return fmt.Errorf("research_trial_ledger kept trial %d violates the frozen acceptance rule", trial.Number)
			}
			bestScore = *trial.Metric
			accepted++
		case "rejected":
			if trial.Decision != "reject" {
				return fmt.Errorf("research_trial_ledger rejected trial %d has an invalid decision", trial.Number)
			}
			if trial.Metric != nil && autoResearchImproved(*trial.Metric, bestScore, spec.Direction, spec.MinDelta) {
				return fmt.Errorf("research_trial_ledger rejected trial %d should have been kept", trial.Number)
			}
		default:
			return fmt.Errorf("research_trial_ledger completed trial %d has invalid status %q", trial.Number, trial.Status)
		}
	}
	if accepted != ledger.AcceptedTrials || math.Float64bits(bestScore) != math.Float64bits(ledger.BestScore) {
		return fmt.Errorf("research_trial_ledger best score or accepted count is inconsistent")
	}
	if ledger.ResourceUsage != nil {
		expectedLedger := ledger
		expectedLedger.ResourceUsage = nil
		finalizeAutoResearchLedgerUsage(&expectedLedger)
		if expectedLedger.ResourceUsage == nil || *ledger.ResourceUsage != *expectedLedger.ResourceUsage {
			return fmt.Errorf("research_trial_ledger resource usage is inconsistent with command evidence")
		}
	}
	return nil
}

func autoResearchBestCandidateFromTask(task *models.Task) (models.ResearchBestCandidate, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "research_best_candidate"))
	if raw == "" {
		return models.ResearchBestCandidate{}, fmt.Errorf("research_best_candidate input is required")
	}
	var best models.ResearchBestCandidate
	if err := json.Unmarshal([]byte(raw), &best); err != nil {
		return best, fmt.Errorf("decode research_best_candidate: %w", err)
	}
	return best, nil
}

func validateAutoResearchBestCandidate(best models.ResearchBestCandidate, ledger models.ResearchTrialLedger, specHash string) error {
	if best.SpecSHA256 != specHash || best.MetricKey != ledger.MetricKey || best.Direction != ledger.Direction || best.AcceptedTrials != ledger.AcceptedTrials {
		return fmt.Errorf("research_best_candidate does not match the frozen spec and trial ledger")
	}
	if math.Float64bits(best.Score) != math.Float64bits(ledger.BestScore) || !equalAutoResearchHashes(best.Files, ledger.BestCandidateFiles) {
		return fmt.Errorf("research_best_candidate score or file hashes do not match the trial ledger")
	}
	return nil
}

func (a *ResearchCodingAgent) proposeAutoResearchCandidate(ctx context.Context, spec models.ResearchSpec, specJSON []byte, ledger models.ResearchTrialLedger, snapshots []autoResearchFileSnapshot) (autoResearchCandidateResponse, error) {
	var response autoResearchCandidateResponse
	contextText := autoResearchSourceContext(snapshots)
	ledgerSummary, _ := json.Marshal(map[string]any{
		"baseline_score": ledger.BaselineScore, "best_score": ledger.BestScore,
		"completed_trials": ledger.CompletedTrials, "accepted_trials": ledger.AcceptedTrials,
		"recent_trials": recentAutoResearchTrials(ledger.Trials, 4),
	})
	message, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.AutoResearchCandidateSystemPrompt},
		{Role: schema.User, Content: prompts.AutoResearchCandidateUserPrompt(string(specJSON), string(ledgerSummary), contextText)},
	})
	if err != nil {
		return response, fmt.Errorf("candidate generation failed: %w", err)
	}
	if err := json.Unmarshal([]byte(cleanJSONResponse(message.Content)), &response); err != nil {
		return response, fmt.Errorf("decode candidate response: %w", err)
	}
	response.Status = strings.ToLower(strings.TrimSpace(response.Status))
	response.Hypothesis = strings.TrimSpace(response.Hypothesis)
	response.Reason = strings.TrimSpace(response.Reason)
	switch response.Status {
	case "propose":
		if response.Hypothesis == "" || len(response.Patches) == 0 || len(response.Patches) > autoResearchMaxPatchFiles {
			return response, fmt.Errorf("candidate proposal requires a hypothesis and 1-%d patches", autoResearchMaxPatchFiles)
		}
	case "stop", "unsupported":
		response.Patches = nil
	default:
		return response, fmt.Errorf("invalid candidate status %q", response.Status)
	}
	return response, nil
}

func applyAutoResearchCandidate(workspacePath string, proposal autoResearchCandidateResponse, best []autoResearchFileSnapshot) ([]models.ResearchPatch, error) {
	allowed := make(map[string]autoResearchFileSnapshot, len(best))
	for _, snapshot := range best {
		allowed[snapshot.Relative] = snapshot
	}
	seen := map[string]struct{}{}
	type pendingPatch struct {
		proposal autoResearchCandidatePatch
		snapshot autoResearchFileSnapshot
		content  []byte
	}
	pending := make([]pendingPatch, 0, len(proposal.Patches))
	for _, patch := range proposal.Patches {
		relative, path, err := autoResearchExistingFile(workspacePath, patch.Path, autoResearchMaxFileBytes, false)
		if err != nil {
			return nil, err
		}
		snapshot, ok := allowed[relative]
		if !ok || path != snapshot.Path {
			return nil, fmt.Errorf("candidate patch targets non-editable file %s", relative)
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("candidate contains duplicate patch %s", relative)
		}
		seen[relative] = struct{}{}
		if strings.TrimSpace(patch.Reason) == "" {
			return nil, fmt.Errorf("candidate patch %s has no reason", relative)
		}
		content := []byte(patch.Content)
		if len(content) == 0 || len(content) > autoResearchMaxFileBytes {
			return nil, fmt.Errorf("candidate patch %s is empty or too large", relative)
		}
		if bytes.Equal(content, snapshot.Content) {
			continue
		}
		if err := validatePaperCodePatchPolicy(string(snapshot.Content), string(content)); err != nil {
			return nil, fmt.Errorf("candidate patch %s violates policy: %w", relative, err)
		}
		pending = append(pending, pendingPatch{proposal: patch, snapshot: snapshot, content: content})
	}
	if len(pending) == 0 {
		return nil, fmt.Errorf("candidate produced no effective editable-file change")
	}

	records := make([]models.ResearchPatch, 0, len(pending))
	for _, patch := range pending {
		if err := writePaperDebugFile(patch.snapshot.Path, patch.content, patch.snapshot.Mode); err != nil {
			if restoreErr := restoreAutoResearchGroups(best); restoreErr != nil {
				return nil, fmt.Errorf("apply candidate failed: %w; restore failed: %v", err, restoreErr)
			}
			return nil, err
		}
		records = append(records, models.ResearchPatch{
			Path: patch.snapshot.Relative, Reason: strings.TrimSpace(patch.proposal.Reason),
			BeforeSHA256: autoResearchSHA(patch.snapshot.Content), AfterSHA256: autoResearchSHA(patch.content),
		})
	}
	return records, nil
}

func (a *ResearchCodingAgent) evaluateAutoResearchCandidate(ctx context.Context, runtimeSession, workspacePath string, spec models.ResearchSpec) (autoResearchEvaluation, error) {
	result := autoResearchEvaluation{Guards: []models.ResearchCommandResult{}}
	if err := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); err != nil {
		return result, err
	}
	for _, command := range spec.GuardCommands {
		commandResult, _, err := a.runAutoResearchCommand(ctx, runtimeSession, command)
		result.Guards = append(result.Guards, commandResult)
		if integrityErr := verifyAutoResearchSpecFiles(workspacePath, spec); integrityErr != nil {
			return result, integrityErr
		}
		if integrityErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); integrityErr != nil {
			return result, integrityErr
		}
		if err != nil {
			return result, fmt.Errorf("guard command failed: %w", err)
		}
	}
	evalResult, stdout, err := a.runAutoResearchCommand(ctx, runtimeSession, spec.EvalCommand)
	result.Eval = evalResult
	if integrityErr := verifyAutoResearchSpecFiles(workspacePath, spec); integrityErr != nil {
		return result, integrityErr
	}
	if integrityErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); integrityErr != nil {
		return result, integrityErr
	}
	if err != nil {
		return result, fmt.Errorf("evaluator failed: %w", err)
	}
	metric, err := parseAutoResearchMetric(stdout, spec.MetricKey)
	if err != nil {
		return result, err
	}
	result.Metric = metric
	return result, nil
}

func (a *ResearchCodingAgent) runAutoResearchCommand(ctx context.Context, runtimeSession string, command []string) (models.ResearchCommandResult, string, error) {
	result := models.ResearchCommandResult{Command: append([]string(nil), command...), ExitCode: -1}
	if err := validateAutoResearchCommand(command); err != nil {
		result.Error = err.Error()
		return result, "", err
	}
	quoted := make([]string, 0, len(command))
	for _, argument := range command {
		quoted = append(quoted, shellEscape(argument))
	}
	shellCommand := "cd /workspace && PYTHONPATH=/workspace:${PYTHONPATH:-} " + strings.Join(quoted, " ")
	started := time.Now()
	response, err := a.Sandbox.ExecCommandStream(ctx, runtimeSession, []string{"bash", "-lc", shellCommand}, func(stream, line string) {
		logToContext(ctx, "[%s] AutoResearch %s: %s", a.Name, stream, line)
	})
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result, "", err
	}
	if response == nil {
		err := fmt.Errorf("sandbox returned nil response")
		result.Error = err.Error()
		return result, "", err
	}
	result.ExitCode = response.ExitCode
	result.StdoutPreview = truncatePaperDebugText(strings.TrimSpace(response.Stdout), autoResearchOutputPreviewBytes)
	result.StderrPreview = truncatePaperDebugText(strings.TrimSpace(response.Stderr), autoResearchOutputPreviewBytes)
	if response.ExitCode != 0 {
		err := fmt.Errorf("command exited with code %d: %s", response.ExitCode, chooseNonEmpty(result.StderrPreview, result.StdoutPreview))
		result.Error = err.Error()
		return result, response.Stdout, err
	}
	return result, response.Stdout, nil
}

func validateAutoResearchCommand(command []string) error {
	if len(command) == 0 || len(command) > 64 {
		return fmt.Errorf("command must contain 1-64 arguments")
	}
	allowed := map[string]struct{}{
		"python": {}, "python3": {}, "pytest": {}, "go": {}, "node": {}, "npm": {},
		"pnpm": {}, "yarn": {}, "cargo": {}, "make": {},
	}
	if _, ok := allowed[strings.TrimSpace(command[0])]; !ok {
		return fmt.Errorf("executable %q is not allowed", command[0])
	}
	for _, argument := range command {
		if argument == "" || len(argument) > 2048 || strings.ContainsAny(argument, "\x00\r\n") {
			return fmt.Errorf("command contains an empty, oversized or multiline argument")
		}
	}
	return nil
}

func validateAutoResearchEvaluatorScope(workspacePath string, command []string, editable map[string]struct{}) error {
	candidates := append([]string(nil), command[1:]...)
	for index, argument := range command[1:] {
		if argument == "-m" && index+2 < len(command) {
			modulePath := strings.ReplaceAll(command[index+2], ".", "/") + ".py"
			candidates = append(candidates, modulePath)
		}
	}
	for _, candidate := range candidates {
		if candidate == "" || strings.HasPrefix(candidate, "-") {
			continue
		}
		relative, _, err := autoResearchExistingFile(workspacePath, candidate, autoResearchMaxProtectedBytes, true)
		if err != nil {
			continue
		}
		if _, mutable := editable[relative]; mutable {
			return fmt.Errorf("eval_command cannot execute editable file %s as its evaluator", relative)
		}
	}
	return nil
}

func autoResearchPathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		set[path] = struct{}{}
	}
	return set
}

func parseAutoResearchMetric(stdout, metricKey string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		var payload map[string]any
		if err := decoder.Decode(&payload); err != nil {
			continue
		}
		value, ok := autoResearchMetricValue(payload, metricKey)
		if !ok {
			continue
		}
		var metric float64
		switch typed := value.(type) {
		case json.Number:
			parsed, err := typed.Float64()
			if err != nil {
				return 0, fmt.Errorf("metric %s is not numeric", metricKey)
			}
			metric = parsed
		case float64:
			metric = typed
		default:
			return 0, fmt.Errorf("metric %s is not numeric", metricKey)
		}
		if math.IsNaN(metric) || math.IsInf(metric, 0) {
			return 0, fmt.Errorf("metric %s is not finite", metricKey)
		}
		return metric, nil
	}
	return 0, fmt.Errorf("evaluator stdout has no final JSON object containing metric %s", metricKey)
}

func autoResearchMetricValue(payload map[string]any, key string) (any, bool) {
	if value, ok := payload[key]; ok {
		return value, true
	}
	var current any = payload
	for _, part := range strings.Split(key, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func normalizeAutoResearchPaths(workspacePath string, paths []string, maximum, maxBytes int, allowScholar bool) ([]string, error) {
	if len(paths) > maximum {
		return nil, fmt.Errorf("at most %d files are allowed", maximum)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, candidate := range paths {
		relative, _, err := autoResearchExistingFile(workspacePath, candidate, maxBytes, allowScholar)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[relative]; duplicate {
			continue
		}
		seen[relative] = struct{}{}
		out = append(out, relative)
	}
	sort.Strings(out)
	return out, nil
}

func autoResearchExistingFile(workspacePath, candidate string, maxBytes int, allowScholar bool) (string, string, error) {
	workspacePath = filepath.Clean(workspacePath)
	candidate = strings.TrimSpace(candidate)
	if strings.HasPrefix(filepath.ToSlash(candidate), "/workspace/") {
		candidate = strings.TrimPrefix(filepath.ToSlash(candidate), "/workspace/")
	}
	if candidate == "" {
		return "", "", fmt.Errorf("empty AutoResearch file path")
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
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("AutoResearch path escapes workspace: %s", candidate)
	}
	relativeSlash := filepath.ToSlash(relative)
	if strings.HasPrefix(relativeSlash, ".git/") || (!allowScholar && strings.HasPrefix(relativeSlash, ".scholar/")) {
		return "", "", fmt.Errorf("AutoResearch path is outside the allowed scope: %s", relativeSlash)
	}
	current := workspacePath
	parts := strings.Split(relativeSlash, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", "", fmt.Errorf("AutoResearch file is unavailable: %s", relativeSlash)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("AutoResearch path contains a symbolic link: %s", relativeSlash)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", "", fmt.Errorf("AutoResearch path parent is not a directory: %s", relativeSlash)
		}
		if index == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() > int64(maxBytes)) {
			return "", "", fmt.Errorf("AutoResearch file %s must be regular and no larger than %d bytes", relativeSlash, maxBytes)
		}
	}
	return relativeSlash, current, nil
}

func snapshotAutoResearchFiles(workspacePath string, files []string, maxBytes int, allowScholar bool) ([]autoResearchFileSnapshot, error) {
	snapshots := make([]autoResearchFileSnapshot, 0, len(files))
	for _, relative := range files {
		normalized, path, err := autoResearchExistingFile(workspacePath, relative, maxBytes, allowScholar)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, autoResearchFileSnapshot{Workspace: workspacePath, Relative: normalized, Path: path, Content: raw, Mode: info.Mode()})
	}
	return snapshots, nil
}

func restoreAutoResearchSnapshots(snapshots []autoResearchFileSnapshot) error {
	for _, snapshot := range snapshots {
		if err := prepareAutoResearchRestorePath(snapshot); err != nil {
			return err
		}
		if info, err := os.Lstat(snapshot.Path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			if err := os.RemoveAll(snapshot.Path); err != nil {
				return err
			}
		}
		if err := writePaperDebugFile(snapshot.Path, snapshot.Content, snapshot.Mode); err != nil {
			return err
		}
	}
	return nil
}

func restoreAutoResearchGroups(groups ...[]autoResearchFileSnapshot) error {
	for _, snapshots := range groups {
		if err := restoreAutoResearchSnapshots(snapshots); err != nil {
			return err
		}
	}
	return nil
}

func restoreAutoResearchWorkspace(workspacePath string, editableFiles []string, immutableSnapshots []autoResearchFileSnapshot, groups ...[]autoResearchFileSnapshot) error {
	expected := make(map[string]struct{}, len(immutableSnapshots))
	for _, snapshot := range immutableSnapshots {
		expected[snapshot.Relative] = struct{}{}
	}
	currentFiles, err := autoResearchImmutableSourceFiles(workspacePath, editableFiles)
	if err != nil {
		return err
	}
	for _, file := range currentFiles {
		if _, existed := expected[file.relative]; existed {
			continue
		}
		boundary := autoResearchFileSnapshot{Workspace: workspacePath, Relative: file.relative, Path: file.path}
		if err := prepareAutoResearchRestorePath(boundary); err != nil {
			return err
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := restoreAutoResearchSnapshots(immutableSnapshots); err != nil {
		return err
	}
	return restoreAutoResearchGroups(groups...)
}

func prepareAutoResearchRestorePath(snapshot autoResearchFileSnapshot) error {
	workspacePath := filepath.Clean(snapshot.Workspace)
	if workspacePath == "." || snapshot.Relative == "" {
		return fmt.Errorf("AutoResearch restore snapshot is missing its workspace boundary")
	}
	expectedPath := filepath.Join(workspacePath, filepath.FromSlash(snapshot.Relative))
	if filepath.Clean(expectedPath) != filepath.Clean(snapshot.Path) {
		return fmt.Errorf("AutoResearch restore path does not match its workspace-relative snapshot")
	}
	current := workspacePath
	parts := strings.Split(filepath.ToSlash(snapshot.Relative), "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("AutoResearch restore parent is not a regular directory: %s", filepath.ToSlash(current))
		}
	}
	return nil
}

func hashAutoResearchFiles(workspacePath string, files []string, maxBytes int, allowScholar bool) ([]models.ResearchFileHash, error) {
	hashes := make([]models.ResearchFileHash, 0, len(files))
	for _, candidate := range files {
		relative, path, err := autoResearchExistingFile(workspacePath, candidate, maxBytes, allowScholar)
		if err != nil {
			return nil, err
		}
		hash, err := sha256File(path)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, models.ResearchFileHash{Path: relative, SHA256: hash})
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i].Path < hashes[j].Path })
	return hashes, nil
}

func verifyAutoResearchSpecFiles(workspacePath string, spec models.ResearchSpec) error {
	current, err := hashAutoResearchFiles(workspacePath, spec.ProtectedFiles, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return fmt.Errorf("%w: protected file unavailable: %v", errAutoResearchIntegrity, err)
	}
	if !equalAutoResearchHashes(current, spec.FrozenProtected) {
		return fmt.Errorf("%w: protected AutoResearch evaluator or data changed", errAutoResearchIntegrity)
	}
	return nil
}

func equalAutoResearchHashes(left, right []models.ResearchFileHash) bool {
	if len(left) != len(right) {
		return false
	}
	leftMap := make(map[string]string, len(left))
	for _, item := range left {
		if _, duplicate := leftMap[item.Path]; duplicate {
			return false
		}
		leftMap[item.Path] = item.SHA256
	}
	rightPaths := make(map[string]struct{}, len(right))
	for _, item := range right {
		if _, duplicate := rightPaths[item.Path]; duplicate {
			return false
		}
		rightPaths[item.Path] = struct{}{}
		if leftMap[item.Path] != item.SHA256 {
			return false
		}
	}
	return true
}

func autoResearchImmutableFingerprint(workspacePath string, editableFiles []string) (string, error) {
	files, err := autoResearchImmutableSourceFiles(workspacePath, editableFiles)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	for _, file := range files {
		hasher.Write([]byte(file.relative))
		hasher.Write([]byte{0})
		hasher.Write([]byte(fmt.Sprint(file.size)))
		hasher.Write([]byte{0})
		handle, err := os.Open(file.path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hasher, handle)
		closeErr := handle.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func autoResearchImmutableSourceFiles(workspacePath string, editableFiles []string) ([]autoResearchSourceFile, error) {
	editable := make(map[string]struct{}, len(editableFiles))
	for _, relative := range editableFiles {
		editable[filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))] = struct{}{}
	}
	files := make([]autoResearchSourceFile, 0, 256)
	var totalBytes int64
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
			switch strings.ToLower(entry.Name()) {
			case ".git", ".scholar", "__pycache__", ".venv", "venv", "node_modules", "dist", "build", "target":
				if relative != "." {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if _, mutable := editable[relativeSlash]; mutable {
			return nil
		}
		if !autoResearchImmutableFile(relativeSlash) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("AutoResearch immutable source cannot be a symbolic link: %s", relativeSlash)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(files) >= autoResearchMaxImmutableFiles || totalBytes+info.Size() > autoResearchMaxImmutableBytes {
			return fmt.Errorf("AutoResearch immutable source fingerprint exceeds %d files or %d bytes", autoResearchMaxImmutableFiles, autoResearchMaxImmutableBytes)
		}
		totalBytes += info.Size()
		files = append(files, autoResearchSourceFile{path: path, relative: relativeSlash, size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	return files, nil
}

func snapshotAutoResearchImmutableFiles(workspacePath string, editableFiles []string) ([]autoResearchFileSnapshot, error) {
	files, err := autoResearchImmutableSourceFiles(workspacePath, editableFiles)
	if err != nil {
		return nil, err
	}
	snapshots := make([]autoResearchFileSnapshot, 0, len(files))
	for _, file := range files {
		raw, err := os.ReadFile(file.path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(file.path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, autoResearchFileSnapshot{
			Workspace: workspacePath, Relative: file.relative, Path: file.path, Content: raw, Mode: info.Mode(),
		})
	}
	return snapshots, nil
}

func autoResearchImmutableFile(relative string) bool {
	base := strings.ToLower(filepath.Base(relative))
	switch base {
	case "makefile", "dockerfile", "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "pyproject.toml", "requirements.txt":
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".py", ".pyi", ".go", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".sh", ".toml", ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func verifyAutoResearchImmutableFingerprint(workspacePath string, editableFiles []string, expected string) error {
	current, err := autoResearchImmutableFingerprint(workspacePath, editableFiles)
	if err != nil {
		return fmt.Errorf("%w: immutable source fingerprint failed: %v", errAutoResearchIntegrity, err)
	}
	if current != expected {
		return fmt.Errorf("%w: non-editable repository source changed during frozen evaluation", errAutoResearchIntegrity)
	}
	return nil
}

func autoResearchSourceContext(snapshots []autoResearchFileSnapshot) string {
	var builder strings.Builder
	for _, snapshot := range snapshots {
		builder.WriteString("FILE: ")
		builder.WriteString(snapshot.Relative)
		builder.WriteString("\n")
		builder.Write(snapshot.Content)
		builder.WriteString("\nEND FILE\n\n")
	}
	return builder.String()
}

func recentAutoResearchTrials(trials []models.ResearchTrial, maximum int) []models.ResearchTrial {
	if len(trials) <= maximum {
		return append([]models.ResearchTrial(nil), trials...)
	}
	return append([]models.ResearchTrial(nil), trials[len(trials)-maximum:]...)
}

func autoResearchImproved(candidate, best float64, direction string, minDelta float64) bool {
	return autoResearchDelta(candidate, best, direction)+1e-12 >= minDelta && autoResearchDelta(candidate, best, direction) > 0
}

func autoResearchDelta(candidate, best float64, direction string) float64 {
	if direction == "minimize" {
		return best - candidate
	}
	return candidate - best
}

func boundedAutoResearchInput(task *models.Task, key string, fallback, minimum, maximum int) int {
	value, ok := optionalBoundedAutoResearchInput(task, key, minimum, maximum)
	if !ok {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func optionalBoundedAutoResearchInput(task *models.Task, key string, minimum, maximum int) (int, bool) {
	raw := strings.TrimSpace(extractTaskInputLike(task, key))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if value < minimum {
		return minimum, true
	}
	if value > maximum {
		return maximum, true
	}
	return value, true
}

func cleanAutoResearchStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func failAutoResearchRun(task *models.Task, ledger *models.ResearchTrialLedger, failure error) error {
	if ledger != nil {
		if ledger.FinishedAt.IsZero() {
			ledger.FinishedAt = time.Now().UTC()
		}
		finalizeAutoResearchLedgerUsage(ledger)
		ledgerJSON, _ := json.Marshal(ledger)
		if task != nil {
			task.Result = string(ledgerJSON)
			task.StructuredData = string(ledgerJSON)
			setResearchCodingArtifacts(task, map[string]string{"research_trial_ledger": string(ledgerJSON)})
		}
	}
	return failResearchCodingTask(task, failure)
}

func finalizeAutoResearchLedgerUsage(ledger *models.ResearchTrialLedger) {
	if ledger == nil {
		return
	}
	usage := models.ResearchResourceUsage{}
	for _, trial := range ledger.Trials {
		for _, command := range trial.GuardResults {
			addAutoResearchCommandUsage(&usage, command, true)
		}
		addAutoResearchCommandUsage(&usage, trial.EvalResult, false)
	}
	usage.WallDurationMS = autoResearchElapsedMS(ledger.StartedAt, ledger.FinishedAt)
	ledger.ResourceUsage = &usage
}

func addAutoResearchCommandUsage(usage *models.ResearchResourceUsage, command models.ResearchCommandResult, guard bool) {
	if usage == nil || len(command.Command) == 0 {
		return
	}
	usage.CommandRuns++
	if guard {
		usage.GuardRuns++
	} else {
		usage.EvaluatorRuns++
	}
	if command.DurationMS > 0 {
		usage.CommandDurationMS += command.DurationMS
	}
	if command.Error == "" && command.ExitCode == 0 {
		usage.SuccessfulCommands++
	} else {
		usage.FailedCommands++
	}
}

func autoResearchElapsedMS(startedAt, finishedAt time.Time) int64 {
	if startedAt.IsZero() || finishedAt.IsZero() || finishedAt.Before(startedAt) {
		return 0
	}
	return finishedAt.Sub(startedAt).Milliseconds()
}

func autoResearchSHA(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
