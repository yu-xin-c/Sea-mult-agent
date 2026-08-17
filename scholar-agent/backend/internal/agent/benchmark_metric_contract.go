package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"scholar-agent-backend/internal/models"
)

const benchmarkPublicEvaluatorPath = ".scholar/benchmark/evaluators/public_evaluator.py"

func (a *BenchmarkAgent) executeContractFreeze(ctx context.Context, task *models.Task) error {
	workspace, err := benchmarkWorkspace(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	audit, err := benchmarkAuditFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	split, splitJSON, err := benchmarkSplitManifestFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if split.DatasetSHA256 != audit.DatasetSHA256 || split.TaskType != audit.TaskType {
		return failBenchmarkTask(task, fmt.Errorf("benchmark split and audit contracts do not match"))
	}
	if err := verifyBenchmarkPublicSplits(workspace, split); err != nil {
		return failBenchmarkTask(task, err)
	}
	metric, err := buildBenchmarkMetricContract(task, audit.TaskType)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	reward := models.BenchmarkRewardContract{
		Version: models.BenchmarkRewardContractVersion, QualityTransform: "baseline_scaled_delta",
		BaselineNormalization: "max_abs_baseline_or_one", DurationPenaltyPerSecond: 0.0001,
		FailurePenalty: 1, Usage: "candidate_priority_only",
	}

	publicCode := renderBenchmarkEvaluator(audit.TaskType, audit.TargetColumn, false)
	publicPath, err := benchmarkPathInWorkspace(workspace, benchmarkPublicEvaluatorPath)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if err := os.MkdirAll(filepath.Dir(publicPath), 0o700); err != nil {
		return failBenchmarkTask(task, err)
	}
	if err := os.WriteFile(publicPath, []byte(publicCode), 0o600); err != nil {
		return failBenchmarkTask(task, err)
	}
	publicHash := sha256.Sum256([]byte(publicCode))
	hiddenHash := ""
	if benchmarkTaskRequiresTarget(audit.TaskType) {
		privateDirectory, privateErr := benchmarkPrivateStateDirectory(split.PrivateStateID, false)
		if privateErr != nil {
			return failBenchmarkTask(task, privateErr)
		}
		hiddenCode := renderBenchmarkEvaluator(audit.TaskType, audit.TargetColumn, true)
		hiddenPath := filepath.Join(privateDirectory, "hidden_evaluator.py")
		if err := os.WriteFile(hiddenPath, []byte(hiddenCode), 0o600); err != nil {
			return failBenchmarkTask(task, err)
		}
		hash := sha256.Sum256([]byte(hiddenCode))
		hiddenHash = hex.EncodeToString(hash[:])
	}
	splitHash := sha256.Sum256([]byte(splitJSON))
	identityPayload, _ := json.Marshal(map[string]any{
		"dataset": split.DatasetSHA256, "split": hex.EncodeToString(splitHash[:]),
		"task": audit.TaskType, "inputs": audit.InputColumns, "target": audit.TargetColumn,
		"metric": metric, "reward": reward,
		"public_evaluator": hex.EncodeToString(publicHash[:]), "hidden_evaluator": hiddenHash,
	})
	identity := sha256.Sum256(identityPayload)
	contract := models.BenchmarkContract{
		Version: models.BenchmarkContractVersion, ID: "benchmark-" + hex.EncodeToString(identity[:10]),
		DatasetSHA256: split.DatasetSHA256, SplitManifestSHA256: hex.EncodeToString(splitHash[:]),
		TaskType: audit.TaskType, InputColumns: append([]string(nil), audit.InputColumns...),
		TargetColumn: audit.TargetColumn, Metric: metric, Reward: reward,
		PublicEvaluatorSHA256: hex.EncodeToString(publicHash[:]), HiddenEvaluatorSHA256: hiddenHash,
		FrozenAt: time.Now().UTC(),
	}
	contractJSON, _ := json.Marshal(contract)
	metricJSON, _ := json.Marshal(metric)
	rewardJSON, _ := json.Marshal(reward)
	contractPath, err := benchmarkPathInWorkspace(workspace, ".scholar/benchmark/contract.json")
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if err := os.WriteFile(contractPath, contractJSON, 0o600); err != nil {
		return failBenchmarkTask(task, err)
	}
	relativeEvaluator, _ := filepath.Rel(workspace, publicPath)
	report := fmt.Sprintf("frozen %s benchmark: primary=%s direction=%s reward=%s", audit.TaskType, metric.PrimaryMetric, metric.Direction, reward.Usage)
	task.Result, task.StructuredData, task.Status = string(contractJSON), string(contractJSON), models.StatusCompleted
	setBenchmarkArtifacts(task, map[string]string{
		"benchmark_contract":              string(contractJSON),
		"benchmark_metric_contract":       string(metricJSON),
		"benchmark_reward_contract":       string(rewardJSON),
		"benchmark_public_evaluator_path": filepath.ToSlash(relativeEvaluator),
		"benchmark_evaluator_manifest":    benchmarkEvaluatorManifestJSON(contract),
		"benchmark_contract_report":       report,
	})
	logToContext(ctx, "[%s] %s", a.Name, report)
	return nil
}

func benchmarkSplitManifestFromTask(task *models.Task) (models.BenchmarkSplitManifest, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_split_manifest"))
	if raw == "" {
		return models.BenchmarkSplitManifest{}, "", fmt.Errorf("benchmark_split_manifest input is required")
	}
	var manifest models.BenchmarkSplitManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return manifest, raw, fmt.Errorf("decode benchmark split manifest: %w", err)
	}
	if manifest.Version != models.BenchmarkSplitVersion || manifest.ID == "" || manifest.DatasetSHA256 == "" || len(manifest.Splits) != 3 {
		return manifest, raw, fmt.Errorf("benchmark split manifest is incomplete")
	}
	for _, name := range []string{"train", "validation", "test"} {
		artifact, ok := manifest.Splits[name]
		if !ok || artifact.SHA256 == "" || artifact.RowCount <= 0 || artifact.RelativePath == "" {
			return manifest, raw, fmt.Errorf("benchmark %s split artifact is incomplete", name)
		}
	}
	return manifest, raw, nil
}

func benchmarkContractFromTask(task *models.Task) (models.BenchmarkContract, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_contract"))
	if raw == "" {
		return models.BenchmarkContract{}, fmt.Errorf("benchmark_contract input is required")
	}
	var contract models.BenchmarkContract
	if err := json.Unmarshal([]byte(raw), &contract); err != nil {
		return contract, fmt.Errorf("decode benchmark contract: %w", err)
	}
	if contract.Version != models.BenchmarkContractVersion || contract.ID == "" || contract.DatasetSHA256 == "" || contract.Metric.PrimaryMetric == "" {
		return contract, fmt.Errorf("benchmark contract is incomplete")
	}
	return contract, nil
}

func buildBenchmarkMetricContract(task *models.Task, taskType string) (models.BenchmarkMetricContract, error) {
	type metricDefaults struct {
		Primary string
		Metrics []models.BenchmarkMetricDefinition
	}
	unitMinimum, unitMaximum := 0.0, 1.0
	defaults := map[string]metricDefaults{
		"classification": {
			Primary: "macro_f1",
			Metrics: []models.BenchmarkMetricDefinition{
				{Name: "macro_f1", Direction: "maximize", Minimum: &unitMinimum, Maximum: &unitMaximum, Description: "unweighted mean F1 over observed classes"},
				{Name: "accuracy", Direction: "maximize", Minimum: &unitMinimum, Maximum: &unitMaximum, Description: "fraction of exact class matches"},
			},
		},
		"regression": {
			Primary: "mae",
			Metrics: []models.BenchmarkMetricDefinition{
				{Name: "mae", Direction: "minimize", Minimum: &unitMinimum, Description: "mean absolute error"},
				{Name: "rmse", Direction: "minimize", Minimum: &unitMinimum, Description: "root mean squared error"},
			},
		},
		"generation": {
			Primary: "exact_match",
			Metrics: []models.BenchmarkMetricDefinition{
				{Name: "exact_match", Direction: "maximize", Minimum: &unitMinimum, Maximum: &unitMaximum, Description: "normalized exact string match"},
				{Name: "token_f1", Direction: "maximize", Minimum: &unitMinimum, Maximum: &unitMaximum, Description: "whitespace-token overlap F1"},
			},
		},
		"retrieval": {
			Primary: "ndcg_at_k",
			Metrics: []models.BenchmarkMetricDefinition{
				{Name: "ndcg_at_k", Direction: "maximize", Minimum: &unitMinimum, Maximum: &unitMaximum, Description: "normalized discounted cumulative gain at the frozen cutoff"},
			},
		},
		"inference": {
			Primary: "p95_ms",
			Metrics: []models.BenchmarkMetricDefinition{
				{Name: "p95_ms", Direction: "minimize", Minimum: &unitMinimum, Description: "95th percentile request latency in milliseconds"},
				{Name: "throughput", Direction: "maximize", Minimum: &unitMinimum, Description: "completed samples per second"},
			},
		},
	}
	selected, ok := defaults[taskType]
	if !ok {
		return models.BenchmarkMetricContract{}, fmt.Errorf("unsupported benchmark task type %q", taskType)
	}
	primary := strings.ToLower(benchmarkTaskString(task, "benchmark_primary_metric"))
	if primary == "" {
		primary = selected.Primary
	}
	direction := ""
	for _, metric := range selected.Metrics {
		if metric.Name == primary {
			direction = metric.Direction
			break
		}
	}
	if direction == "" {
		return models.BenchmarkMetricContract{}, fmt.Errorf("metric %q is unsupported for %s", primary, taskType)
	}
	minDelta, err := benchmarkOptionalTaskFloat(task, "benchmark_min_delta", 0, 1e12, 0.0001)
	if err != nil {
		return models.BenchmarkMetricContract{}, err
	}
	target, err := benchmarkOptionalTaskFloatPointer(task, "benchmark_target_score", -1e12, 1e12)
	if err != nil {
		return models.BenchmarkMetricContract{}, err
	}
	return models.BenchmarkMetricContract{
		Version: models.BenchmarkMetricContractVersion, TaskType: taskType,
		PrimaryMetric: primary, Direction: direction, MinDelta: minDelta, TargetScore: target,
		Metrics: selected.Metrics, Aggregation: "deterministic_full_split",
		ValidationRuns:   boundedTaskInt(task, "benchmark_validation_runs", 1, 1, 5),
		EvaluatorVersion: "benchmark.evaluator/v1",
	}, nil
}

func benchmarkOptionalTaskFloat(task *models.Task, key string, minimum, maximum, fallback float64) (float64, error) {
	raw := benchmarkTaskString(task, key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !isFiniteBenchmarkNumber(value) || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be between %g and %g", key, minimum, maximum)
	}
	return value, nil
}

func benchmarkOptionalTaskFloatPointer(task *models.Task, key string, minimum, maximum float64) (*float64, error) {
	raw := benchmarkTaskString(task, key)
	if raw == "" {
		return nil, nil
	}
	value, err := benchmarkOptionalTaskFloat(task, key, minimum, maximum, 0)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func verifyBenchmarkPublicSplits(workspace string, split models.BenchmarkSplitManifest) error {
	for _, name := range []string{"train", "validation", "test"} {
		artifact := split.Splits[name]
		path, err := benchmarkPathInWorkspace(workspace, artifact.RelativePath)
		if err != nil {
			return err
		}
		actual, err := benchmarkRegularFileSHA(path, name+" split")
		if err != nil || actual != artifact.SHA256 {
			return fmt.Errorf("benchmark %s split hash changed", name)
		}
	}
	return nil
}

func benchmarkEvaluatorManifestJSON(contract models.BenchmarkContract) string {
	payload, _ := json.Marshal(map[string]any{
		"version": "benchmark.evaluator-manifest/v1", "contract_id": contract.ID,
		"public_evaluator_sha256":  contract.PublicEvaluatorSHA256,
		"hidden_evaluator_sha256":  contract.HiddenEvaluatorSHA256,
		"hidden_evaluator_exposed": false,
	})
	return string(payload)
}

func renderBenchmarkEvaluator(taskType, targetColumn string, hidden bool) string {
	mode := "public"
	if hidden {
		mode = "hidden"
	}
	return fmt.Sprintf(`#!/usr/bin/env python3
import argparse
import json
import math
import re
from collections import Counter

TASK_TYPE = %q
TARGET_COLUMN = %q
MODE = %q

def read_jsonl(path):
    rows = []
    with open(path, "r", encoding="utf-8") as handle:
        for line in handle:
            if line.strip():
                rows.append(json.loads(line))
    return rows

def normalize(value):
    return " ".join(str(value).strip().lower().split())

def classification(pairs):
    labels = sorted(set([str(p) for p, _ in pairs] + [str(t) for _, t in pairs]))
    accuracy = sum(str(p) == str(t) for p, t in pairs) / len(pairs)
    scores = []
    for label in labels:
        tp = sum(str(p) == label and str(t) == label for p, t in pairs)
        fp = sum(str(p) == label and str(t) != label for p, t in pairs)
        fn = sum(str(p) != label and str(t) == label for p, t in pairs)
        scores.append(0.0 if 2 * tp + fp + fn == 0 else 2 * tp / (2 * tp + fp + fn))
    return {"macro_f1": sum(scores) / len(scores), "accuracy": accuracy}

def regression(pairs):
    errors = [float(p) - float(t) for p, t in pairs]
    return {"mae": sum(abs(v) for v in errors) / len(errors), "rmse": math.sqrt(sum(v * v for v in errors) / len(errors))}

def generation(pairs):
    exact = 0
    token_f1 = 0.0
    for prediction, target in pairs:
        p, t = normalize(prediction), normalize(target)
        exact += p == t
        pc, tc = Counter(p.split()), Counter(t.split())
        overlap = sum((pc & tc).values())
        if not pc and not tc:
            token_f1 += 1.0
        elif overlap:
            precision, recall = overlap / sum(pc.values()), overlap / sum(tc.values())
            token_f1 += 2 * precision * recall / (precision + recall)
    return {"exact_match": exact / len(pairs), "token_f1": token_f1 / len(pairs)}

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--predictions", required=True)
    parser.add_argument("--labels", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    predictions, labels = read_jsonl(args.predictions), read_jsonl(args.labels)
    label_by_id = {str(row.get("id") or row.get("__benchmark_id")): row.get("target", row.get(TARGET_COLUMN)) for row in labels}
    pairs = []
    for index, row in enumerate(predictions):
        identity = str(row.get("id") or row.get("__benchmark_id") or "")
        target = label_by_id.get(identity) if identity else labels[index].get("target", labels[index].get(TARGET_COLUMN))
        pairs.append((row["prediction"], target))
    if not pairs or len(pairs) != len(labels):
        raise ValueError("prediction and label counts differ")
    if TASK_TYPE == "classification":
        metrics = classification(pairs)
    elif TASK_TYPE == "regression":
        metrics = regression(pairs)
    elif TASK_TYPE == "generation":
        metrics = generation(pairs)
    else:
        raise ValueError("task requires a domain evaluator")
    with open(args.output, "w", encoding="utf-8") as handle:
        json.dump(metrics, handle, sort_keys=True, allow_nan=False)

if __name__ == "__main__":
    main()
`, taskType, targetColumn, mode)
}

func (a *BenchmarkAgent) executeHiddenValidation(ctx context.Context, task *models.Task) error {
	workspace, err := benchmarkWorkspace(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	contract, err := benchmarkContractFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	split, splitJSON, err := benchmarkSplitManifestFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if contract.DatasetSHA256 != split.DatasetSHA256 {
		return failBenchmarkTask(task, fmt.Errorf("benchmark contract and split manifest do not match"))
	}
	splitHash := sha256.Sum256([]byte(splitJSON))
	if hex.EncodeToString(splitHash[:]) != contract.SplitManifestSHA256 {
		return failBenchmarkTask(task, fmt.Errorf("benchmark split manifest changed after contract freeze"))
	}
	if err := verifyBenchmarkPublicSplits(workspace, split); err != nil {
		return failBenchmarkTask(task, err)
	}
	publicEvaluator, err := benchmarkPathInWorkspace(workspace, benchmarkPublicEvaluatorPath)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if actual, hashErr := benchmarkRegularFileSHA(publicEvaluator, "public evaluator"); hashErr != nil || actual != contract.PublicEvaluatorSHA256 {
		return failBenchmarkTask(task, fmt.Errorf("public evaluator changed after contract freeze"))
	}
	publicMetrics := map[string]float64{}
	if raw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_run_metrics")); raw != "" {
		publicMetrics, err = decodeBenchmarkMetrics([]byte(raw))
		if err != nil {
			return failBenchmarkTask(task, err)
		}
	}
	if _, ok := benchmarkMetric(publicMetrics, contract.Metric.PrimaryMetric); !ok {
		return failBenchmarkTask(task, fmt.Errorf("public run omitted frozen primary metric %q", contract.Metric.PrimaryMetric))
	}

	if !benchmarkTaskRequiresTarget(contract.TaskType) {
		primary, ok := benchmarkMetric(publicMetrics, contract.Metric.PrimaryMetric)
		if !ok || !isFiniteBenchmarkNumber(primary) {
			return failBenchmarkTask(task, fmt.Errorf("public run omitted primary metric %q", contract.Metric.PrimaryMetric))
		}
		report := models.BenchmarkValidationReport{
			Version: models.BenchmarkValidationVersion, Status: "validated", ContractID: contract.ID,
			DatasetSHA256: contract.DatasetSHA256, PrimaryMetric: contract.Metric.PrimaryMetric,
			Direction: contract.Metric.Direction, PublicMetrics: publicMetrics, HiddenMetrics: publicMetrics,
			HiddenSampleCount: 0, TargetReached: benchmarkMetricTargetReached(contract.Metric, primary),
			ProtectedFilesValid: true,
			Checks:              []string{"public split hashes matched", "public evaluator hash matched", "performance metric was finite; timing remains adapter-reported"},
			ValidatedAt:         time.Now().UTC(),
		}
		return finishBenchmarkValidation(task, report)
	}

	privateDirectory, err := benchmarkPrivateStateDirectory(split.PrivateStateID, false)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	labelsPath := filepath.Join(privateDirectory, "test_labels.jsonl")
	if actual, hashErr := benchmarkRegularFileSHA(labelsPath, "hidden labels"); hashErr != nil || actual != split.HiddenLabelsSHA {
		return failBenchmarkTask(task, fmt.Errorf("hidden labels changed after split freeze"))
	}
	hiddenEvaluator := filepath.Join(privateDirectory, "hidden_evaluator.py")
	if actual, hashErr := benchmarkRegularFileSHA(hiddenEvaluator, "hidden evaluator"); hashErr != nil || actual != contract.HiddenEvaluatorSHA256 {
		return failBenchmarkTask(task, fmt.Errorf("hidden evaluator changed after contract freeze"))
	}
	predictionsRelative := strings.TrimSpace(extractTaskInputLike(task, "benchmark_hidden_predictions_path"))
	if predictionsRelative == "" {
		return failBenchmarkTask(task, fmt.Errorf("benchmark_hidden_predictions_path input is required"))
	}
	predictionsPath, err := benchmarkPathInWorkspace(workspace, predictionsRelative)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	hiddenRunRaw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_hidden_run_manifest"))
	var hiddenRun models.BenchmarkRunManifest
	if hiddenRunRaw == "" || json.Unmarshal([]byte(hiddenRunRaw), &hiddenRun) != nil {
		return failBenchmarkTask(task, fmt.Errorf("benchmark_hidden_run_manifest input is invalid"))
	}
	if hiddenRun.Status != "ok" || hiddenRun.DatasetSHA256 != split.Splits["test"].SHA256 || hiddenRun.SampleCount != split.Splits["test"].RowCount {
		return failBenchmarkTask(task, fmt.Errorf("hidden run manifest does not match the frozen test split"))
	}
	predictions, err := readBenchmarkHiddenPredictions(predictionsPath)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	labels, err := readBenchmarkHiddenLabels(labelsPath)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if len(predictions) != split.Splits["test"].RowCount || len(labels) != split.Splits["test"].RowCount {
		return failBenchmarkTask(task, fmt.Errorf("hidden prediction or label count does not match frozen test split"))
	}
	pairs, err := joinBenchmarkHiddenPairs(predictions, labels)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	hiddenMetrics, err := recomputeBenchmarkContractMetrics(contract.TaskType, pairs)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	primary, ok := benchmarkMetric(hiddenMetrics, contract.Metric.PrimaryMetric)
	if !ok {
		return failBenchmarkTask(task, fmt.Errorf("hidden evaluator omitted primary metric %q", contract.Metric.PrimaryMetric))
	}
	targetReached := benchmarkMetricTargetReached(contract.Metric, primary)
	status := "validated"
	if contract.Metric.TargetScore != nil && !targetReached {
		status = "not_validated"
	}
	report := models.BenchmarkValidationReport{
		Version: models.BenchmarkValidationVersion, Status: status, ContractID: contract.ID,
		DatasetSHA256: contract.DatasetSHA256, PrimaryMetric: contract.Metric.PrimaryMetric,
		Direction: contract.Metric.Direction, PublicMetrics: publicMetrics, HiddenMetrics: hiddenMetrics,
		HiddenSampleCount: len(pairs), TargetReached: targetReached, ProtectedFilesValid: true,
		Checks: []string{
			"public split and evaluator hashes matched", "hidden labels and evaluator hashes matched",
			"prediction IDs matched every hidden label exactly once", "metrics were recomputed by the backend",
		}, ValidatedAt: time.Now().UTC(),
	}
	logToContext(ctx, "[%s] hidden benchmark %s=%0.6f status=%s", a.Name, contract.Metric.PrimaryMetric, primary, status)
	return finishBenchmarkValidation(task, report)
}

func finishBenchmarkValidation(task *models.Task, report models.BenchmarkValidationReport) error {
	payload, _ := json.Marshal(report)
	metrics, _ := json.Marshal(report.HiddenMetrics)
	task.Result, task.StructuredData, task.Status = string(payload), string(payload), models.StatusCompleted
	setBenchmarkArtifacts(task, map[string]string{
		"benchmark_metrics":           string(metrics),
		"benchmark_validation_report": string(payload),
	})
	return nil
}

type benchmarkHiddenPrediction struct {
	ID         string
	Prediction any
}

func readBenchmarkHiddenPredictions(path string) ([]benchmarkHiddenPrediction, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("hidden predictions are unavailable")
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	result := make([]benchmarkHiddenPrediction, 0, 64)
	seen := map[string]struct{}{}
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("decode hidden prediction %d: %w", len(result)+1, err)
		}
		id := strings.TrimSpace(fmt.Sprint(firstBenchmarkValue(row, "id", "__benchmark_id")))
		prediction, ok := row["prediction"]
		if id == "" || !ok || prediction == nil {
			return nil, fmt.Errorf("hidden prediction %d must contain id and prediction", len(result)+1)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("hidden prediction id %q is duplicated", id)
		}
		seen[id] = struct{}{}
		result = append(result, benchmarkHiddenPrediction{ID: id, Prediction: prediction})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func firstBenchmarkValue(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok {
			return value
		}
	}
	return ""
}

func readBenchmarkHiddenLabels(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	labels := map[string]string{}
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var row map[string]string
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, err
		}
		id := strings.TrimSpace(row["id"])
		if id == "" || labels[id] != "" {
			return nil, fmt.Errorf("hidden label identity is missing or duplicated")
		}
		labels[id] = row["target"]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return labels, nil
}

func joinBenchmarkHiddenPairs(predictions []benchmarkHiddenPrediction, labels map[string]string) ([]benchmarkPredictionRecord, error) {
	pairs := make([]benchmarkPredictionRecord, 0, len(predictions))
	seen := map[string]struct{}{}
	for _, prediction := range predictions {
		target, ok := labels[prediction.ID]
		if !ok {
			return nil, fmt.Errorf("prediction id %q is absent from hidden labels", prediction.ID)
		}
		seen[prediction.ID] = struct{}{}
		pairs = append(pairs, benchmarkPredictionRecord{Prediction: prediction.Prediction, Target: target})
	}
	if len(seen) != len(labels) {
		return nil, fmt.Errorf("hidden predictions did not cover every frozen test id")
	}
	return pairs, nil
}

func recomputeBenchmarkContractMetrics(taskType string, pairs []benchmarkPredictionRecord) (map[string]float64, error) {
	if len(pairs) == 0 {
		return nil, fmt.Errorf("hidden prediction set is empty")
	}
	switch taskType {
	case "classification":
		accuracy, macroF1 := recomputeClassificationMetrics(pairs)
		return map[string]float64{"accuracy": accuracy, "macro_f1": macroF1}, nil
	case "regression":
		mse, mae, err := recomputeRegressionMetrics(pairs)
		if err != nil {
			return nil, err
		}
		return map[string]float64{"mae": mae, "rmse": math.Sqrt(mse)}, nil
	case "generation":
		exact, tokenF1 := 0, 0.0
		for _, pair := range pairs {
			prediction := normalizeBenchmarkGeneratedText(fmt.Sprint(pair.Prediction))
			target := normalizeBenchmarkGeneratedText(fmt.Sprint(pair.Target))
			if prediction == target {
				exact++
			}
			tokenF1 += benchmarkTokenF1(prediction, target)
		}
		return map[string]float64{"exact_match": float64(exact) / float64(len(pairs)), "token_f1": tokenF1 / float64(len(pairs))}, nil
	default:
		return nil, fmt.Errorf("task type %q requires a domain-specific hidden evaluator", taskType)
	}
}

func normalizeBenchmarkGeneratedText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func benchmarkTokenF1(prediction, target string) float64 {
	predicted := strings.Fields(prediction)
	expected := strings.Fields(target)
	if len(predicted) == 0 && len(expected) == 0 {
		return 1
	}
	if len(predicted) == 0 || len(expected) == 0 {
		return 0
	}
	predictedCounts, expectedCounts := map[string]int{}, map[string]int{}
	for _, token := range predicted {
		predictedCounts[token]++
	}
	for _, token := range expected {
		expectedCounts[token]++
	}
	overlap := 0
	for token, count := range predictedCounts {
		overlap += min(count, expectedCounts[token])
	}
	if overlap == 0 {
		return 0
	}
	precision := float64(overlap) / float64(len(predicted))
	recall := float64(overlap) / float64(len(expected))
	return 2 * precision * recall / (precision + recall)
}

func benchmarkMetricTargetReached(contract models.BenchmarkMetricContract, score float64) bool {
	if contract.TargetScore == nil {
		return true
	}
	if contract.Direction == "minimize" {
		return score <= *contract.TargetScore
	}
	return score >= *contract.TargetScore
}

func sortedBenchmarkMetricNames(metrics map[string]float64) []string {
	keys := make([]string, 0, len(metrics))
	for key := range metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
