package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

func (a *ResearchCodingAgent) executeAdapterPreflight(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("benchmark sandbox is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	manifest, manifestJSON, err := benchmarkDatasetManifestFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, _, err := benchmarkAdapterSpecFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	code := strings.TrimSpace(extractTaskInputLike(task, "benchmark_generated_code"))
	if code == "" {
		return failResearchCodingTask(task, fmt.Errorf("benchmark generated code input is required"))
	}
	if err := validateBenchmarkAdapterCode(code); err != nil {
		return failResearchCodingTask(task, err)
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	datasetRelative, err := locateMaterializedBenchmarkDataset(workspacePath, manifest.Name)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	maxAttempts := boundedTaskInt(task, "benchmark_max_preflight_attempts", 3, 1, 3)
	attempts := make([]models.BenchmarkAttempt, 0, maxAttempts)
	var validated models.BenchmarkHarnessReport
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		codeHash := sha256.Sum256([]byte(code))
		spec.AdapterCodeSHA256 = hex.EncodeToString(codeHash[:])
		spec.RepairAttempts = attempt - 1
		specJSONBytes, _ := json.Marshal(spec)
		if _, _, err := writeBenchmarkAdapterFiles(workspacePath, code, specJSONBytes); err != nil {
			return failResearchCodingTask(task, err)
		}

		report, runErr := a.runBenchmarkHarness(ctx, runtimeSession, workspacePath, datasetRelative, manifest, "preflight", 8)
		entry := models.BenchmarkAttempt{Attempt: attempt, ExitCode: 0}
		if runErr == nil {
			entry.Repaired = attempt > 1
			attempts = append(attempts, entry)
			validated = report
			break
		}
		entry.ExitCode = 1
		entry.Error = truncateBenchmarkText(runErr.Error(), 4000)
		entry.Repaired = attempt > 1
		attempts = append(attempts, entry)
		if strings.Contains(runErr.Error(), "repository files changed outside .scholar") {
			return failResearchCodingTask(task, runErr)
		}
		if attempt == maxAttempts || a.ChatModel == nil {
			break
		}
		repaired, repairReason, repairErr := a.repairBenchmarkAdapter(ctx, manifestJSON, string(specJSONBytes), code, runErr.Error())
		if repairErr != nil {
			attempts[len(attempts)-1].Error += "; repair failed: " + repairErr.Error()
			break
		}
		logToContext(ctx, "[%s] benchmark adapter repair %d: %s", a.Name, attempt, repairReason)
		code = repaired
	}
	if validated.Status != "passed" {
		report := models.BenchmarkHarnessReport{Status: "failed", Mode: "preflight", Attempts: attempts, Reason: "adapter did not pass bounded preflight"}
		payload, _ := json.Marshal(report)
		task.Result = string(payload)
		return failResearchCodingTask(task, fmt.Errorf("benchmark adapter preflight failed after %d attempt(s)", len(attempts)))
	}

	validated.Attempts = attempts
	validatedPayload, _ := json.Marshal(validated)
	spec.Status = "preflight_passed"
	spec.RepairAttempts = len(attempts) - 1
	codeHash := sha256.Sum256([]byte(code))
	spec.AdapterCodeSHA256 = hex.EncodeToString(codeHash[:])
	validatedSpec, _ := json.Marshal(spec)
	adapterPath, _, err := writeBenchmarkAdapterFiles(workspacePath, code, validatedSpec)
	if err != nil {
		return failResearchCodingTask(task, err)
	}

	task.Code = code
	task.Result = string(validatedPayload)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"validated_benchmark_adapter_spec":   string(validatedSpec),
		"validated_benchmark_generated_code": code,
		"validated_benchmark_code_file_path": adapterPath,
		"benchmark_preflight_report":         string(validatedPayload),
	})
	return nil
}

func (a *ResearchCodingAgent) executeBenchmark(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("benchmark sandbox is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	manifest, _, err := benchmarkDatasetManifestFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, _, err := benchmarkAdapterSpecFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if spec.Status != "preflight_passed" {
		return failResearchCodingTask(task, fmt.Errorf("benchmark adapter has not passed preflight"))
	}
	code := strings.TrimSpace(extractTaskInputLike(task, "validated_benchmark_generated_code"))
	if err := validateBenchmarkAdapterCode(code); err != nil {
		return failResearchCodingTask(task, err)
	}
	codeHash := sha256.Sum256([]byte(code))
	if hex.EncodeToString(codeHash[:]) != spec.AdapterCodeSHA256 {
		return failResearchCodingTask(task, fmt.Errorf("validated adapter code hash mismatch"))
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	datasetRelative, err := locateMaterializedBenchmarkDataset(workspacePath, manifest.Name)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	limit := boundedTaskInt(task, "benchmark_max_samples", benchmarkMinInt(manifest.RowCount, 1000), 1, 100000)
	if limit > manifest.RowCount {
		limit = manifest.RowCount
	}
	report, err := a.runBenchmarkHarness(ctx, runtimeSession, workspacePath, datasetRelative, manifest, "run", limit)
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("benchmark execution failed: %w", err))
	}
	payload, _ := json.Marshal(report)
	metricsPayload, _ := json.Marshal(report.Metrics)
	runManifestPath, _ := benchmarkPathInWorkspace(workspacePath, benchmarkAdapterDirectory+"/run/run_manifest.json")
	runManifest, err := readRegularBenchmarkFile(runManifestPath, "run_manifest.json", 1024*1024)
	if err != nil {
		return failResearchCodingTask(task, err)
	}

	task.Result = string(payload)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"benchmark_run_metrics":      string(metricsPayload),
		"benchmark_run_manifest":     string(runManifest),
		"benchmark_predictions_path": report.PredictionsPath,
		"benchmark_execution_report": string(payload),
	})
	return nil
}

func (a *ResearchCodingAgent) executeBenchmarkValidation(ctx context.Context, task *models.Task) error {
	manifest, _, err := benchmarkDatasetManifestFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	metricsRaw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_run_metrics"))
	runManifestRaw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_run_manifest"))
	metrics, err := decodeBenchmarkMetrics([]byte(metricsRaw))
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("invalid benchmark metrics: %w", err))
	}
	var runManifest models.BenchmarkRunManifest
	if err := json.Unmarshal([]byte(runManifestRaw), &runManifest); err != nil {
		return failResearchCodingTask(task, fmt.Errorf("invalid benchmark run manifest: %w", err))
	}
	if runManifest.Status != "ok" || runManifest.DatasetSHA256 != manifest.SHA256 || runManifest.SampleCount <= 0 || runManifest.SampleCount > manifest.RowCount {
		return failResearchCodingTask(task, fmt.Errorf("benchmark run manifest does not match the dataset contract"))
	}
	for name, value := range metrics {
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e12 {
			return failResearchCodingTask(task, fmt.Errorf("metric %s is outside the accepted numeric range", name))
		}
	}
	report := map[string]any{
		"status":         "validated",
		"dataset_sha256": manifest.SHA256,
		"sample_count":   runManifest.SampleCount,
		"metrics":        metrics,
		"checks": []string{
			"dataset hash matched",
			"sample count matched",
			"metrics were finite numeric values",
		},
	}
	reportPayload, _ := json.Marshal(report)
	metricsPayload, _ := json.Marshal(metrics)
	task.Result = string(reportPayload)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"benchmark_metrics":           string(metricsPayload),
		"benchmark_validation_report": string(reportPayload),
	})
	logToContext(ctx, "[%s] benchmark validated with %d metric(s)", a.Name, len(metrics))
	return nil
}

func (a *ResearchCodingAgent) runBenchmarkHarness(ctx context.Context, sandboxID, workspacePath, datasetRelative string, manifest models.DatasetManifest, mode string, limit int) (models.BenchmarkHarnessReport, error) {
	outputRelative := benchmarkAdapterDirectory + "/" + mode
	outputPath, err := benchmarkPathInWorkspace(workspacePath, outputRelative)
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	if err := os.RemoveAll(outputPath); err != nil {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("clear benchmark output: %w", err)
	}
	if err := os.MkdirAll(outputPath, 0o700); err != nil {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("create benchmark output: %w", err)
	}
	datasetPath, err := benchmarkPathInWorkspace(workspacePath, datasetRelative)
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	datasetHashBefore, err := benchmarkRegularFileSHA(datasetPath, "benchmark dataset")
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	if datasetHashBefore != manifest.SHA256 {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("benchmark dataset hash changed before execution")
	}
	adapterPath, err := benchmarkPathInWorkspace(workspacePath, benchmarkAdapterFile)
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	adapterHashBefore, err := benchmarkRegularFileSHA(adapterPath, "benchmark adapter")
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	before, err := benchmarkRepositoryFingerprint(workspacePath)
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	containerDataset := "/workspace/" + filepath.ToSlash(datasetRelative)
	containerOutput := "/workspace/" + filepath.ToSlash(outputRelative)
	command := []string{
		"bash", "-lc", fmt.Sprintf(
			"cd /workspace && PYTHONPATH=/workspace:${PYTHONPATH:-} python3 %s --dataset %s --output-dir %s --limit %d --repo-root /workspace --seed 17",
			shellEscape(benchmarkAdapterFile), shellEscape(containerDataset), shellEscape(containerOutput), limit,
		),
	}
	result, execErr := a.Sandbox.ExecCommandStream(ctx, sandboxID, command, func(stream, line string) {
		logToContext(ctx, "[%s] benchmark %s: %s", a.Name, stream, line)
	})
	after, fingerprintErr := benchmarkRepositoryFingerprint(workspacePath)
	if fingerprintErr != nil {
		return models.BenchmarkHarnessReport{}, fingerprintErr
	}
	if before != after {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("repository files changed outside .scholar during benchmark execution")
	}
	datasetHashAfter, err := benchmarkRegularFileSHA(datasetPath, "benchmark dataset")
	if err != nil || datasetHashAfter != datasetHashBefore {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("benchmark dataset changed during execution")
	}
	adapterHashAfter, err := benchmarkRegularFileSHA(adapterPath, "benchmark adapter")
	if err != nil || adapterHashAfter != adapterHashBefore {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("benchmark adapter changed itself during execution")
	}
	if execErr != nil {
		return models.BenchmarkHarnessReport{}, execErr
	}
	if result == nil {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("sandbox returned no benchmark result")
	}
	if result.ExitCode != 0 {
		failure := strings.TrimSpace(result.Stderr)
		if failure == "" {
			failure = strings.TrimSpace(result.Stdout)
		}
		return models.BenchmarkHarnessReport{}, fmt.Errorf("adapter exited with code %d: %s", result.ExitCode, truncateBenchmarkText(failure, 12000))
	}
	return validateBenchmarkOutputDirectory(workspacePath, outputRelative, manifest, limit, mode)
}

func (a *ResearchCodingAgent) repairBenchmarkAdapter(ctx context.Context, manifestJSON, specJSON, code, failure string) (string, string, error) {
	message, err := a.ChatModel.Generate(ctx, []*schema.Message{
		{Role: schema.System, Content: prompts.BenchmarkAdapterSystemPrompt},
		{Role: schema.User, Content: prompts.BenchmarkAdapterRepairUserPrompt(manifestJSON, specJSON, code, truncateBenchmarkText(failure, 12000))},
	})
	if err != nil {
		return "", "", err
	}
	var response benchmarkAdapterRepairResponse
	if err := json.Unmarshal([]byte(cleanJSONResponse(message.Content)), &response); err != nil {
		return "", "", fmt.Errorf("parse benchmark adapter repair: %w", err)
	}
	response.AdapterCode = strings.TrimSpace(response.AdapterCode)
	if response.AdapterCode == code {
		return "", "", fmt.Errorf("repair returned unchanged adapter")
	}
	if err := validateBenchmarkAdapterCode(response.AdapterCode); err != nil {
		return "", "", err
	}
	return response.AdapterCode, strings.TrimSpace(response.Reason), nil
}

func validateBenchmarkOutputDirectory(workspacePath, outputRelative string, manifest models.DatasetManifest, limit int, mode string) (models.BenchmarkHarnessReport, error) {
	outputPath, err := benchmarkPathInWorkspace(workspacePath, outputRelative)
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	metricsRaw, err := readRegularBenchmarkFile(filepath.Join(outputPath, "metrics.json"), "metrics.json", 1024*1024)
	if err != nil {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("adapter did not write metrics.json")
	}
	metrics, err := decodeBenchmarkMetrics(metricsRaw)
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	manifestRaw, err := readRegularBenchmarkFile(filepath.Join(outputPath, "run_manifest.json"), "run_manifest.json", 1024*1024)
	if err != nil {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("adapter did not write run_manifest.json")
	}
	var runManifest models.BenchmarkRunManifest
	if err := json.Unmarshal(manifestRaw, &runManifest); err != nil {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("decode run_manifest.json: %w", err)
	}
	if runManifest.Status != "ok" {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("adapter run status is %q", runManifest.Status)
	}
	if runManifest.DatasetSHA256 != manifest.SHA256 {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("adapter dataset hash mismatch")
	}
	if runManifest.SampleCount <= 0 || runManifest.SampleCount > limit || runManifest.SampleCount > manifest.RowCount {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("adapter sample count %d violates limit %d", runManifest.SampleCount, limit)
	}
	predictionsPath := filepath.Join(outputPath, "predictions.jsonl")
	predictions, err := readBenchmarkPredictionLines(predictionsPath, manifest.TargetColumn != "", runManifest.SampleCount)
	if err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	if len(predictions) != runManifest.SampleCount {
		return models.BenchmarkHarnessReport{}, fmt.Errorf("prediction count %d does not match sample count %d", len(predictions), runManifest.SampleCount)
	}
	if err := validateBenchmarkMetricsAgainstPredictions(metrics, predictions, manifest.SuggestedTask); err != nil {
		return models.BenchmarkHarnessReport{}, err
	}
	relPredictions, _ := filepath.Rel(workspacePath, predictionsPath)
	return models.BenchmarkHarnessReport{
		Status: "passed", Mode: mode, SampleCount: runManifest.SampleCount, Metrics: metrics, PredictionsPath: filepath.ToSlash(relPredictions),
	}, nil
}

func decodeBenchmarkMetrics(raw []byte) (map[string]float64, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return nil, fmt.Errorf("decode metrics.json: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode metrics.json: trailing content")
	}
	metrics := map[string]float64{}
	for key, rawValue := range values {
		var value float64
		switch typed := rawValue.(type) {
		case json.Number:
			parsed, err := typed.Float64()
			if err != nil {
				return nil, fmt.Errorf("metric %s is not numeric", key)
			}
			value = parsed
		case float64:
			value = typed
		default:
			return nil, fmt.Errorf("metric %s is not numeric", key)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("metric %s is not finite", key)
		}
		metrics[key] = value
	}
	if len(metrics) == 0 {
		return nil, fmt.Errorf("metrics.json is empty")
	}
	return metrics, nil
}

type benchmarkPredictionRecord struct {
	Prediction any
	Target     any
}

func readBenchmarkPredictionLines(path string, requireTarget bool, maxRecords int) ([]benchmarkPredictionRecord, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("adapter did not write a regular predictions.jsonl file")
	}
	if info.Size() > 256*1024*1024 {
		return nil, fmt.Errorf("predictions.jsonl exceeds 256 MiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("adapter did not write predictions.jsonl")
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	records := make([]benchmarkPredictionRecord, 0, 64)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(line), &object); err != nil {
			return nil, fmt.Errorf("prediction line %d is not JSON", len(records)+1)
		}
		prediction, hasPrediction := object["prediction"]
		if !hasPrediction || prediction == nil {
			return nil, fmt.Errorf("prediction line %d is missing prediction", len(records)+1)
		}
		target, hasTarget := object["target"]
		if requireTarget && (!hasTarget || target == nil) {
			return nil, fmt.Errorf("prediction line %d is missing target", len(records)+1)
		}
		records = append(records, benchmarkPredictionRecord{Prediction: prediction, Target: target})
		if maxRecords > 0 && len(records) > maxRecords {
			return nil, fmt.Errorf("prediction count exceeds declared sample count %d", maxRecords)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func validateBenchmarkMetricsAgainstPredictions(metrics map[string]float64, predictions []benchmarkPredictionRecord, taskType string) error {
	if len(predictions) == 0 {
		return fmt.Errorf("predictions.jsonl is empty")
	}
	switch taskType {
	case "classification":
		accuracy, macroF1 := recomputeClassificationMetrics(predictions)
		validated := false
		if reported, ok := benchmarkMetric(metrics, "accuracy"); ok {
			validated = true
			if !benchmarkMetricClose(reported, accuracy) {
				return fmt.Errorf("reported accuracy %.8f does not match predictions %.8f", reported, accuracy)
			}
		}
		if reported, ok := benchmarkMetric(metrics, "macro_f1"); ok {
			validated = true
			if !benchmarkMetricClose(reported, macroF1) {
				return fmt.Errorf("reported macro_f1 %.8f does not match predictions %.8f", reported, macroF1)
			}
		}
		if !validated {
			return fmt.Errorf("classification benchmark must report accuracy or macro_f1")
		}
	case "regression":
		mse, mae, err := recomputeRegressionMetrics(predictions)
		if err != nil {
			return err
		}
		validated := false
		if reported, ok := benchmarkMetric(metrics, "mse"); ok {
			validated = true
			if !benchmarkMetricClose(reported, mse) {
				return fmt.Errorf("reported mse %.8f does not match predictions %.8f", reported, mse)
			}
		}
		if reported, ok := benchmarkMetric(metrics, "mae"); ok {
			validated = true
			if !benchmarkMetricClose(reported, mae) {
				return fmt.Errorf("reported mae %.8f does not match predictions %.8f", reported, mae)
			}
		}
		if !validated {
			return fmt.Errorf("regression benchmark must report mse or mae")
		}
	}
	return nil
}

func recomputeClassificationMetrics(predictions []benchmarkPredictionRecord) (float64, float64) {
	labels := map[string]struct{}{}
	tp := map[string]int{}
	fp := map[string]int{}
	fn := map[string]int{}
	correct := 0
	for _, record := range predictions {
		prediction := fmt.Sprint(record.Prediction)
		target := fmt.Sprint(record.Target)
		labels[prediction] = struct{}{}
		labels[target] = struct{}{}
		if prediction == target {
			correct++
			tp[target]++
		} else {
			fp[prediction]++
			fn[target]++
		}
	}
	macroF1 := 0.0
	for label := range labels {
		denominator := float64(2*tp[label] + fp[label] + fn[label])
		if denominator > 0 {
			macroF1 += float64(2*tp[label]) / denominator
		}
	}
	if len(labels) > 0 {
		macroF1 /= float64(len(labels))
	}
	return float64(correct) / float64(len(predictions)), macroF1
}

func recomputeRegressionMetrics(predictions []benchmarkPredictionRecord) (float64, float64, error) {
	mse := 0.0
	mae := 0.0
	for index, record := range predictions {
		prediction, err := strconv.ParseFloat(fmt.Sprint(record.Prediction), 64)
		if err != nil {
			return 0, 0, fmt.Errorf("prediction line %d is not numeric", index+1)
		}
		target, err := strconv.ParseFloat(fmt.Sprint(record.Target), 64)
		if err != nil {
			return 0, 0, fmt.Errorf("prediction target line %d is not numeric", index+1)
		}
		delta := prediction - target
		mse += delta * delta
		mae += math.Abs(delta)
	}
	return mse / float64(len(predictions)), mae / float64(len(predictions)), nil
}

func benchmarkMetric(metrics map[string]float64, name string) (float64, bool) {
	for key, value := range metrics {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return value, true
		}
	}
	return 0, false
}

func benchmarkMetricClose(reported, recomputed float64) bool {
	tolerance := math.Max(1e-6, math.Abs(recomputed)*1e-4)
	return math.Abs(reported-recomputed) <= tolerance
}

func readRegularBenchmarkFile(path, name string, maxBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("adapter did not write a regular %s file", name)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, maxBytes)
	}
	return os.ReadFile(path)
}

func benchmarkRegularFileSHA(path, name string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is not a regular file", name)
	}
	return sha256File(path)
}

func benchmarkRuntimeSession(task *models.Task) (string, error) {
	runtimeSession := chooseNonEmpty(
		extractTaskInputLike(task, "prepared_runtime"),
		extractTaskInputLike(task, "runtime_session"),
		extractTaskInputLike(task, "runtime_env"),
	)
	if !strings.HasPrefix(runtimeSession, "dk-") && !strings.HasPrefix(runtimeSession, "os-") {
		return "", fmt.Errorf("valid prepared_runtime input is required")
	}
	return runtimeSession, nil
}

func locateMaterializedBenchmarkDataset(workspacePath, originalName string) (string, error) {
	directory, err := benchmarkPathInWorkspace(workspacePath, ".scholar/uploads")
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", fmt.Errorf("workspace dataset directory is unavailable")
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if strings.HasSuffix(entry.Name(), "-"+filepath.Base(originalName)) || entry.Name() == filepath.Base(originalName) {
			path := filepath.Join(directory, entry.Name())
			relative, _ := filepath.Rel(workspacePath, path)
			return filepath.ToSlash(relative), nil
		}
	}
	return "", fmt.Errorf("uploaded dataset %q was not materialized in the workspace", originalName)
}

func benchmarkRepositoryFingerprint(workspacePath string) (string, error) {
	hasher := sha256.New()
	paths := make([]string, 0, 256)
	err := filepath.WalkDir(workspacePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if relative == ".git" || relative == ".scholar" || strings.HasPrefix(filepath.ToSlash(relative), ".git/") || strings.HasPrefix(filepath.ToSlash(relative), ".scholar/") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		relative, _ := filepath.Rel(workspacePath, path)
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		writeBenchmarkHash(hasher, filepath.ToSlash(relative))
		writeBenchmarkHash(hasher, info.Mode().String())
		writeBenchmarkHash(hasher, fmt.Sprintf("%d", info.Size()))
		writeBenchmarkHash(hasher, fmt.Sprintf("%d", info.ModTime().UnixNano()))
		if info.Size() <= 2*1024*1024 {
			raw, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			hasher.Write(raw)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func writeBenchmarkHash(hasher hash.Hash, value string) {
	hasher.Write([]byte(value))
	hasher.Write([]byte{0})
}
