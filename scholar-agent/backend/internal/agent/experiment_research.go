package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"scholar-agent-backend/internal/models"
)

const experimentMaxOutputBytes = 5 * 1024 * 1024

func (a *ResearchCodingAgent) executeExperimentDatasetPrepare(ctx context.Context, task *models.Task) error {
	files, err := benchmarkUploadsFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("prepare research dataset: %w", err))
	}
	adapter, err := selectExperimentDomainAdapter(task, files)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	workspacePath, manifest, err := adapter.Prepare(ctx, task, files)
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("%s dataset adaptation failed: %w", adapter.Name(), err))
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	report := fmt.Sprintf("adapted %s research data with %s; counts=%v; split=%s", manifest.Domain, manifest.Adapter, manifest.Counts, manifest.SplitMethod)
	task.Result = string(payload)
	task.StructuredData = string(payload)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"workspace_path":              workspacePath,
		"experiment_dataset_manifest": string(payload),
		"experiment_dataset_report":   report,
	})
	logToContext(ctx, "[%s] %s", a.Name, report)
	return nil
}

func (a *ResearchCodingAgent) executeExperimentSpec(ctx context.Context, task *models.Task) error {
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	manifest, err := experimentManifestFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	adapter, err := experimentAdapterByName(manifest.Adapter)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, err := adapter.BuildSpec(task, workspacePath, manifest)
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("freeze experiment spec: %w", err))
	}
	if err := validateExperimentSpec(workspacePath, spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	dependencies, _ := json.Marshal(spec.Dependencies)
	report := fmt.Sprintf("frozen %s experiment contract: %d strategy branches, %d trials, %ds, metric=%s", spec.Domain, len(spec.Strategies), spec.MaxTrials, spec.MaxWallSeconds, spec.MetricKey)
	task.Result = string(payload)
	task.StructuredData = string(payload)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"experiment_spec":        string(payload),
		"experiment_spec_report": report,
		"dependency_spec":        string(dependencies),
	})
	logToContext(ctx, "[%s] %s", a.Name, report)
	return nil
}

func experimentAdapterByName(name string) (experimentDomainAdapter, error) {
	for _, adapter := range experimentDomainAdapters {
		if adapter.Name() == name {
			return adapter, nil
		}
	}
	return nil, fmt.Errorf("experiment adapter %q is not registered", name)
}

func experimentManifestFromTask(task *models.Task) (models.ExperimentDatasetManifest, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "experiment_dataset_manifest"))
	if raw == "" {
		return models.ExperimentDatasetManifest{}, fmt.Errorf("experiment_dataset_manifest input is required")
	}
	var manifest models.ExperimentDatasetManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return manifest, fmt.Errorf("decode experiment dataset manifest: %w", err)
	}
	if manifest.Version != models.ExperimentDatasetVersion || manifest.Domain == "" || manifest.Adapter == "" || len(manifest.FrozenFiles) == 0 {
		return manifest, fmt.Errorf("experiment dataset manifest is incomplete")
	}
	return manifest, nil
}

func experimentSpecFromTask(task *models.Task, workspacePath string) (models.ExperimentResearchSpec, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "experiment_spec"))
	if raw == "" {
		return models.ExperimentResearchSpec{}, "", fmt.Errorf("experiment_spec input is required")
	}
	var spec models.ExperimentResearchSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return spec, "", fmt.Errorf("decode experiment spec: %w", err)
	}
	if err := validateExperimentSpec(workspacePath, spec); err != nil {
		return spec, "", err
	}
	hash := sha256.Sum256([]byte(raw))
	return spec, hex.EncodeToString(hash[:]), nil
}

func validateExperimentSpec(workspacePath string, spec models.ExperimentResearchSpec) error {
	if spec.Version != models.ExperimentSpecVersion || spec.Domain == "" || spec.Adapter == "" || spec.CandidateKind != "strategy_config" {
		return fmt.Errorf("experiment spec identity is incomplete")
	}
	if spec.MetricKey == "" || (spec.Direction != "maximize" && spec.Direction != "minimize") {
		return fmt.Errorf("experiment spec metric or direction is invalid")
	}
	if spec.MinDelta < 0 || spec.MaxTrials < 1 || spec.MaxTrials > 40 || spec.MaxWallSeconds < 30 || spec.MaxWallSeconds > 3600 || spec.ValidationRuns < 1 || spec.ValidationRuns > 5 {
		return fmt.Errorf("experiment spec budget is outside supported bounds")
	}
	if len(spec.Strategies) == 0 || len(spec.Strategies) > 16 {
		return fmt.Errorf("experiment spec must declare 1 to 16 strategy branches")
	}
	if err := validateExperimentCommand(spec.SearchCommand); err != nil {
		return fmt.Errorf("search command: %w", err)
	}
	if err := validateExperimentCommand(spec.HoldoutCommand); err != nil {
		return fmt.Errorf("holdout command: %w", err)
	}
	if slices.Equal(spec.SearchCommand, spec.HoldoutCommand) {
		return fmt.Errorf("search and holdout commands must select different frozen evaluation data")
	}
	strategyNames := map[string]struct{}{}
	for _, strategy := range spec.Strategies {
		if strategy.Name == "" {
			return fmt.Errorf("experiment strategy name is required")
		}
		if _, exists := strategyNames[strategy.Name]; exists {
			return fmt.Errorf("duplicate experiment strategy %q", strategy.Name)
		}
		strategyNames[strategy.Name] = struct{}{}
		parameterNames := map[string]struct{}{}
		for _, parameter := range strategy.Parameters {
			if parameter.Name == "" || len(parameter.Values) == 0 || len(parameter.Values) > 32 {
				return fmt.Errorf("strategy %q has an invalid parameter domain", strategy.Name)
			}
			if _, exists := parameterNames[parameter.Name]; exists {
				return fmt.Errorf("strategy %q repeats parameter %q", strategy.Name, parameter.Name)
			}
			parameterNames[parameter.Name] = struct{}{}
			if !experimentValueInDomain(parameter.Default, parameter.Values) {
				return fmt.Errorf("strategy %q parameter %q default is outside its domain", strategy.Name, parameter.Name)
			}
		}
	}
	if len(spec.FrozenFiles) == 0 {
		return fmt.Errorf("experiment spec has no protected assets")
	}
	return verifyExperimentFiles(workspacePath, spec.FrozenFiles)
}

func validateExperimentCommand(command []string) error {
	if len(command) < 2 || len(command) > 32 {
		return fmt.Errorf("command must contain 2 to 32 argv items")
	}
	placeholders := 0
	for _, item := range command {
		if strings.ContainsAny(item, "\x00\r\n") {
			return fmt.Errorf("command contains an invalid control character")
		}
		placeholders += strings.Count(item, "{config_path}")
		cleaned := strings.ReplaceAll(item, "{config_path}", "")
		if strings.Contains(cleaned, "{") || strings.Contains(cleaned, "}") {
			return fmt.Errorf("unsupported command placeholder")
		}
	}
	if placeholders != 1 {
		return fmt.Errorf("command must contain exactly one {config_path} placeholder")
	}
	return nil
}

func experimentValueInDomain(value any, domain []any) bool {
	wanted, _ := json.Marshal(value)
	for _, item := range domain {
		candidate, _ := json.Marshal(item)
		if string(wanted) == string(candidate) {
			return true
		}
	}
	return false
}

func (a *ResearchCodingAgent) executeExperimentRun(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("experiment sandbox is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, specHash, err := experimentSpecFromTask(task, workspacePath)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	started := time.Now().UTC()
	ledger := models.ExperimentTrialLedger{
		Version: models.ExperimentLedgerVersion, SpecSHA256: specHash, Status: "running",
		Domain: spec.Domain, Adapter: spec.Adapter, MetricKey: spec.MetricKey, Direction: spec.Direction,
		TargetScore: spec.TargetScore, MaxTrials: spec.MaxTrials,
		StrategySpace: append([]models.ExperimentStrategy(nil), spec.Strategies...),
		Trials:        []models.ExperimentTrial{}, StartedAt: started,
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.MaxWallSeconds)*time.Second)
	defer cancel()

	baseline := defaultExperimentCandidate(spec.Strategies[0], "baseline default")
	baselineStart := time.Now().UTC()
	baselineEvaluation, baselineDuration, err := a.evaluateExperimentCandidate(runCtx, runtimeSession, workspacePath, spec, baseline, false)
	baselineTrial := models.ExperimentTrial{
		Number: 0, Candidate: baseline, Status: "baseline", Decision: "keep",
		Reason: "frozen default establishes the baseline", DurationMS: baselineDuration,
		StartedAt: baselineStart, FinishedAt: time.Now().UTC(),
	}
	ledger.ResourceUsage.EvaluatorRuns++
	ledger.ResourceUsage.EvaluatorTimeMS += baselineDuration
	if err != nil {
		baselineTrial.Status, baselineTrial.Decision, baselineTrial.Error = "failed", "abort", err.Error()
		ledger.Trials = append(ledger.Trials, baselineTrial)
		ledger.Status, ledger.StopReason, ledger.FinishedAt = "failed", "baseline_failed", time.Now().UTC()
		return failExperimentRun(task, ledger, err)
	}
	baselineScore := baselineEvaluation.Metrics[spec.MetricKey]
	baselineTrial.Metrics = baselineEvaluation.Metrics
	baselineTrial.Score = experimentFloatPointer(baselineScore)
	ledger.Trials = append(ledger.Trials, baselineTrial)
	ledger.BaselineScore, ledger.BestScore = baselineScore, baselineScore
	ledger.BestCandidate, ledger.BestEvaluation = baseline, baselineEvaluation
	logToContext(ctx, "[%s] experiment baseline %s=%.6f strategy=%s", a.Name, spec.MetricKey, baselineScore, baseline.Strategy)

	queue := make([]models.ExperimentCandidate, 0, spec.MaxTrials*2)
	seen := map[string]struct{}{baseline.ID: {}}
	for _, strategy := range spec.Strategies[1:] {
		candidate := defaultExperimentCandidate(strategy, "compare method branch against the baseline")
		experimentEnqueueCandidate(&queue, seen, candidate)
	}
	for _, candidate := range refineExperimentCandidate(spec, baseline) {
		experimentEnqueueCandidate(&queue, seen, candidate)
	}
	if experimentTargetReached(spec.Direction, spec.TargetScore, baselineScore) {
		ledger.StopReason = "baseline_target_reached"
	}

	for ledger.CompletedTrials < spec.MaxTrials && ledger.StopReason == "" {
		if runCtx.Err() != nil {
			ledger.StopReason = "wall_time_budget_exhausted"
			break
		}
		if len(queue) == 0 {
			ledger.StopReason = "candidate_space_exhausted"
			break
		}
		candidate := queue[0]
		queue = queue[1:]
		trial := models.ExperimentTrial{
			Number: ledger.CompletedTrials + 1, Candidate: candidate, Status: "running", Decision: "reject",
			Reason: candidate.Reason, StartedAt: time.Now().UTC(),
		}
		evaluation, duration, evalErr := a.evaluateExperimentCandidate(runCtx, runtimeSession, workspacePath, spec, candidate, false)
		trial.DurationMS = duration
		ledger.ResourceUsage.EvaluatorRuns++
		ledger.ResourceUsage.EvaluatorTimeMS += duration
		if evalErr != nil {
			trial.Status, trial.Reason, trial.Error = "rejected", "candidate evaluator failed", evalErr.Error()
		} else {
			score := evaluation.Metrics[spec.MetricKey]
			delta := experimentDelta(spec.Direction, score, ledger.BestScore)
			trial.Metrics, trial.Score, trial.DeltaFromBest = evaluation.Metrics, experimentFloatPointer(score), experimentFloatPointer(delta)
			if delta >= spec.MinDelta {
				trial.Status, trial.Decision = "kept", "keep"
				trial.Reason = fmt.Sprintf("%s improved by %.6f (required %.6f)", spec.MetricKey, delta, spec.MinDelta)
				ledger.BestScore, ledger.BestCandidate, ledger.BestEvaluation = score, candidate, evaluation
				ledger.AcceptedTrials++
				for _, child := range refineExperimentCandidate(spec, candidate) {
					experimentEnqueueCandidate(&queue, seen, child)
				}
			} else {
				trial.Status = "rejected"
				trial.Reason = fmt.Sprintf("%s delta %.6f did not meet %.6f", spec.MetricKey, delta, spec.MinDelta)
			}
			if candidate.Depth == 0 {
				for _, child := range refineExperimentCandidate(spec, candidate) {
					experimentEnqueueCandidate(&queue, seen, child)
				}
			}
		}
		trial.FinishedAt = time.Now().UTC()
		ledger.Trials = append(ledger.Trials, trial)
		ledger.CompletedTrials++
		scoreText := "n/a"
		if trial.Score != nil {
			scoreText = fmt.Sprintf("%.6f", *trial.Score)
		}
		logToContext(ctx, "[%s] experiment trial %d %s strategy=%s score=%s", a.Name, trial.Number, trial.Status, candidate.Strategy, scoreText)
		if trial.Status == "kept" && experimentTargetReached(spec.Direction, spec.TargetScore, ledger.BestScore) {
			ledger.StopReason = "target_score_reached"
		}
	}
	if ledger.StopReason == "" {
		ledger.StopReason = "trial_budget_exhausted"
	}
	ledger.Status, ledger.FinishedAt = "completed", time.Now().UTC()
	ledger.ResourceUsage.WallDurationMS = ledger.FinishedAt.Sub(started).Milliseconds()
	best := models.ExperimentBestCandidate{
		SpecSHA256: specHash, Score: ledger.BestScore, MetricKey: spec.MetricKey,
		Candidate: ledger.BestCandidate, Evaluation: ledger.BestEvaluation,
	}
	ledgerJSON, _ := json.Marshal(ledger)
	bestJSON, _ := json.Marshal(best)
	report := fmt.Sprintf("experiment completed %d/%d trials; kept %d; %s %.6f -> %.6f; best=%s; stop=%s", ledger.CompletedTrials, spec.MaxTrials, ledger.AcceptedTrials, spec.MetricKey, ledger.BaselineScore, ledger.BestScore, best.Candidate.Strategy, ledger.StopReason)
	task.Result, task.StructuredData, task.Status = string(ledgerJSON), string(ledgerJSON), models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"experiment_trial_ledger":   string(ledgerJSON),
		"experiment_best_candidate": string(bestJSON),
		"experiment_run_report":     report,
	})
	return nil
}

func failExperimentRun(task *models.Task, ledger models.ExperimentTrialLedger, err error) error {
	payload, _ := json.Marshal(ledger)
	if task != nil {
		task.Result, task.StructuredData = string(payload), string(payload)
	}
	return failResearchCodingTask(task, err)
}

func defaultExperimentCandidate(strategy models.ExperimentStrategy, reason string) models.ExperimentCandidate {
	parameters := make(map[string]any, len(strategy.Parameters))
	for _, parameter := range strategy.Parameters {
		parameters[parameter.Name] = parameter.Default
	}
	candidate := models.ExperimentCandidate{Strategy: strategy.Name, Parameters: parameters, Depth: 0, Reason: reason}
	candidate.ID = experimentCandidateID(candidate.Strategy, candidate.Parameters)
	return candidate
}

func refineExperimentCandidate(spec models.ExperimentResearchSpec, parent models.ExperimentCandidate) []models.ExperimentCandidate {
	var strategy *models.ExperimentStrategy
	for index := range spec.Strategies {
		if spec.Strategies[index].Name == parent.Strategy {
			strategy = &spec.Strategies[index]
			break
		}
	}
	if strategy == nil {
		return nil
	}
	children := []models.ExperimentCandidate{}
	for _, parameter := range strategy.Parameters {
		current := parent.Parameters[parameter.Name]
		currentIndex := -1
		for index, value := range parameter.Values {
			if experimentValueInDomain(current, []any{value}) {
				currentIndex = index
				break
			}
		}
		if currentIndex < 0 {
			continue
		}
		for _, nextIndex := range []int{currentIndex - 1, currentIndex + 1} {
			if nextIndex < 0 || nextIndex >= len(parameter.Values) {
				continue
			}
			parameters := copyExperimentParameters(parent.Parameters)
			parameters[parameter.Name] = parameter.Values[nextIndex]
			candidate := models.ExperimentCandidate{
				ParentID: parent.ID, Strategy: parent.Strategy, Parameters: parameters,
				Depth: parent.Depth + 1, ChangedParameter: parameter.Name,
				Reason: fmt.Sprintf("one-factor ablation: %s %v -> %v", parameter.Name, current, parameter.Values[nextIndex]),
			}
			candidate.ID = experimentCandidateID(candidate.Strategy, candidate.Parameters)
			children = append(children, candidate)
		}
	}
	return children
}

func experimentCandidateID(strategy string, parameters map[string]any) string {
	payload, _ := json.Marshal(struct {
		Strategy   string         `json:"strategy"`
		Parameters map[string]any `json:"parameters"`
	}{strategy, parameters})
	hash := sha256.Sum256(payload)
	return "candidate-" + hex.EncodeToString(hash[:6])
}

func copyExperimentParameters(source map[string]any) map[string]any {
	copyValues := make(map[string]any, len(source))
	for key, value := range source {
		copyValues[key] = value
	}
	return copyValues
}

func experimentEnqueueCandidate(queue *[]models.ExperimentCandidate, seen map[string]struct{}, candidate models.ExperimentCandidate) {
	if _, exists := seen[candidate.ID]; exists {
		return
	}
	seen[candidate.ID] = struct{}{}
	*queue = append(*queue, candidate)
}

func (a *ResearchCodingAgent) evaluateExperimentCandidate(ctx context.Context, runtimeSession, workspacePath string, spec models.ExperimentResearchSpec, candidate models.ExperimentCandidate, holdout bool) (models.ExperimentEvaluation, int64, error) {
	if err := verifyExperimentFiles(workspacePath, spec.FrozenFiles); err != nil {
		return models.ExperimentEvaluation{}, 0, err
	}
	runtimeDirectory, err := benchmarkPathInWorkspace(workspacePath, experimentWorkspaceDirectory+"/runtime")
	if err != nil {
		return models.ExperimentEvaluation{}, 0, err
	}
	if err := os.MkdirAll(runtimeDirectory, 0o700); err != nil {
		return models.ExperimentEvaluation{}, 0, err
	}
	configPath := filepath.Join(runtimeDirectory, candidate.ID+".json")
	configPayload, err := json.Marshal(candidate)
	if err != nil {
		return models.ExperimentEvaluation{}, 0, err
	}
	if err := os.WriteFile(configPath, configPayload, 0o600); err != nil {
		return models.ExperimentEvaluation{}, 0, err
	}
	containerConfig := "/workspace/" + filepath.ToSlash(experimentWorkspaceDirectory+"/runtime/"+candidate.ID+".json")
	commandTemplate := spec.SearchCommand
	mode := "search"
	if holdout {
		commandTemplate, mode = spec.HoldoutCommand, "holdout"
	}
	command := make([]string, len(commandTemplate))
	for index, item := range commandTemplate {
		command[index] = strings.ReplaceAll(item, "{config_path}", containerConfig)
	}
	started := time.Now()
	result, execErr := a.Sandbox.ExecCommandStream(ctx, runtimeSession, command, func(stream, line string) {
		logToContext(ctx, "[%s] experiment %s %s: %s", a.Name, mode, stream, line)
	})
	duration := time.Since(started).Milliseconds()
	if integrityErr := verifyExperimentFiles(workspacePath, spec.FrozenFiles); integrityErr != nil {
		return models.ExperimentEvaluation{}, duration, integrityErr
	}
	if execErr != nil {
		return models.ExperimentEvaluation{}, duration, execErr
	}
	if result == nil {
		return models.ExperimentEvaluation{}, duration, fmt.Errorf("experiment evaluator returned no result")
	}
	if result.ExitCode != 0 {
		failure := chooseNonEmpty(strings.TrimSpace(result.Stderr), strings.TrimSpace(result.Stdout), "unknown evaluator failure")
		return models.ExperimentEvaluation{}, duration, fmt.Errorf("experiment evaluator exited with code %d: %s", result.ExitCode, truncateBenchmarkText(failure, 12000))
	}
	if len(result.Stdout) > experimentMaxOutputBytes {
		return models.ExperimentEvaluation{}, duration, fmt.Errorf("experiment evaluator output exceeds 5 MiB")
	}
	evaluation, err := decodeExperimentEvaluation(result.Stdout)
	if err != nil {
		return evaluation, duration, err
	}
	if err := validateExperimentEvaluation(evaluation, candidate, spec); err != nil {
		return evaluation, duration, err
	}
	return evaluation, duration, nil
}

func decodeExperimentEvaluation(stdout string) (models.ExperimentEvaluation, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var evaluation models.ExperimentEvaluation
		if err := json.Unmarshal([]byte(line), &evaluation); err == nil {
			return evaluation, nil
		}
	}
	return models.ExperimentEvaluation{}, fmt.Errorf("experiment evaluator did not emit the required JSON object")
}

func validateExperimentEvaluation(evaluation models.ExperimentEvaluation, candidate models.ExperimentCandidate, spec models.ExperimentResearchSpec) error {
	if evaluation.Version != models.ExperimentEvaluationVersion || evaluation.CandidateID != candidate.ID || evaluation.Strategy != candidate.Strategy {
		return fmt.Errorf("experiment evaluator identity does not match the candidate")
	}
	wantedParameters, _ := json.Marshal(candidate.Parameters)
	observedParameters, _ := json.Marshal(evaluation.Parameters)
	if string(wantedParameters) != string(observedParameters) {
		return fmt.Errorf("experiment evaluator parameters do not match the candidate")
	}
	if evaluation.CaseCount <= 0 || len(evaluation.AssetHashes) == 0 {
		return fmt.Errorf("experiment evaluator omitted case count or asset hashes")
	}
	score, ok := evaluation.Metrics[spec.MetricKey]
	if !ok || math.IsNaN(score) || math.IsInf(score, 0) {
		return fmt.Errorf("experiment evaluator omitted finite metric %q", spec.MetricKey)
	}
	frozenHashes := map[string]struct{}{}
	for _, item := range spec.FrozenFiles {
		frozenHashes[strings.ToLower(item.SHA256)] = struct{}{}
	}
	for name, hash := range evaluation.AssetHashes {
		if _, ok := frozenHashes[strings.ToLower(hash)]; !ok {
			return fmt.Errorf("experiment evaluator reported an unfrozen asset hash for %s", name)
		}
	}
	if len(evaluation.Evidence) > 100 {
		return fmt.Errorf("experiment evaluator evidence exceeds 100 cases")
	}
	for name, value := range evaluation.Metrics {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("experiment metric %s is not finite", name)
		}
	}
	return nil
}

func experimentDelta(direction string, candidate, current float64) float64 {
	if direction == "minimize" {
		return current - candidate
	}
	return candidate - current
}

func experimentTargetReached(direction string, target *float64, score float64) bool {
	if target == nil {
		return false
	}
	if direction == "minimize" {
		return score <= *target
	}
	return score >= *target
}

func experimentFloatPointer(value float64) *float64 { return &value }

func (a *ResearchCodingAgent) executeExperimentValidation(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("experiment sandbox is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, specHash, err := experimentSpecFromTask(task, workspacePath)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	ledgerRaw := strings.TrimSpace(extractTaskInputLike(task, "experiment_trial_ledger"))
	bestRaw := strings.TrimSpace(extractTaskInputLike(task, "experiment_best_candidate"))
	var ledger models.ExperimentTrialLedger
	var best models.ExperimentBestCandidate
	if json.Unmarshal([]byte(ledgerRaw), &ledger) != nil || ledger.Version != models.ExperimentLedgerVersion || ledger.SpecSHA256 != specHash {
		return failResearchCodingTask(task, fmt.Errorf("experiment trial ledger does not match the frozen spec"))
	}
	if json.Unmarshal([]byte(bestRaw), &best) != nil || best.SpecSHA256 != specHash || best.Candidate.ID != ledger.BestCandidate.ID {
		return failResearchCodingTask(task, fmt.Errorf("experiment best candidate does not match the trial ledger"))
	}
	ledgerHashBytes := sha256.Sum256([]byte(ledgerRaw))
	ledgerHash := hex.EncodeToString(ledgerHashBytes[:])
	baseline := defaultExperimentCandidate(spec.Strategies[0], "holdout baseline")
	protectedIntact := verifyExperimentFiles(workspacePath, spec.FrozenFiles) == nil
	runs := make([]models.ExperimentValidationRun, 0, spec.ValidationRuns)
	passed := 0
	executionFailed := false
	for number := 1; number <= spec.ValidationRuns && protectedIntact; number++ {
		started := time.Now()
		baselineEvaluation, baselineDuration, baselineErr := a.evaluateExperimentCandidate(ctx, runtimeSession, workspacePath, spec, baseline, true)
		candidateEvaluation, candidateDuration, candidateErr := a.evaluateExperimentCandidate(ctx, runtimeSession, workspacePath, spec, best.Candidate, true)
		run := models.ExperimentValidationRun{Number: number, Status: "failed", DurationMS: time.Since(started).Milliseconds()}
		if baselineErr != nil || candidateErr != nil {
			executionFailed = true
			run.Error = chooseNonEmpty(experimentErrorText(baselineErr), experimentErrorText(candidateErr))
			run.DurationMS = baselineDuration + candidateDuration
			runs = append(runs, run)
			continue
		}
		run.BaselineScore = baselineEvaluation.Metrics[spec.MetricKey]
		run.CandidateScore = candidateEvaluation.Metrics[spec.MetricKey]
		run.Delta = experimentDelta(spec.Direction, run.CandidateScore, run.BaselineScore)
		run.Evidence = candidateEvaluation.Evidence
		target := spec.HoldoutTargetScore
		run.TargetReached = target == nil || experimentTargetReached(spec.Direction, target, run.CandidateScore)
		notRegressed := run.Delta >= -1e-9
		if run.TargetReached && notRegressed {
			run.Status = "passed"
			passed++
		} else if !run.TargetReached {
			run.Error = "holdout target was not reached"
		} else {
			run.Error = "best search candidate regressed on holdout"
		}
		runs = append(runs, run)
	}
	protectedIntact = protectedIntact && verifyExperimentFiles(workspacePath, spec.FrozenFiles) == nil
	status := "not_validated"
	if executionFailed || !protectedIntact {
		status = "failed"
	} else if passed == spec.ValidationRuns {
		status = "validated"
	}
	report := models.ExperimentValidationReport{
		Version: models.ExperimentValidationVersion, Status: status, SpecSHA256: specHash, LedgerSHA256: ledgerHash,
		Domain: spec.Domain, Adapter: spec.Adapter, MetricKey: spec.MetricKey,
		SearchBaseline: ledger.BaselineScore, SearchBest: ledger.BestScore, HoldoutTarget: spec.HoldoutTargetScore,
		RequestedRuns: spec.ValidationRuns, PassedRuns: passed, ProtectedIntact: protectedIntact,
		Runs: runs, ValidatedAt: time.Now().UTC(),
		Summary: fmt.Sprintf("%s: search %.6f -> %.6f; holdout passed %d/%d fresh runs", status, ledger.BaselineScore, ledger.BestScore, passed, spec.ValidationRuns),
	}
	payload, _ := json.Marshal(report)
	metrics := map[string]any{
		"metric": spec.MetricKey, "search_baseline": ledger.BaselineScore, "search_best": ledger.BestScore,
		"holdout_passed_runs": passed, "holdout_requested_runs": spec.ValidationRuns, "status": status,
	}
	metricsPayload, _ := json.Marshal(metrics)
	task.Result, task.StructuredData = string(payload), string(payload)
	setResearchCodingArtifacts(task, map[string]string{
		"experiment_validation_report": string(payload),
		"experiment_best_metrics":      string(metricsPayload),
	})
	if status == "failed" {
		return failResearchCodingTask(task, fmt.Errorf("%s", report.Summary))
	}
	task.Status = models.StatusCompleted
	return nil
}

func experimentErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sortedExperimentMetricKeys(metrics map[string]float64) []string {
	keys := make([]string, 0, len(metrics))
	for key := range metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
