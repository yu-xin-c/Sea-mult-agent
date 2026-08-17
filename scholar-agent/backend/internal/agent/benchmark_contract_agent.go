package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"scholar-agent-backend/internal/models"
)

const (
	benchmarkSplitDirectory = ".scholar/benchmark/dataset"
	benchmarkMaxSplitRows   = 100000
	benchmarkNearPairLimit  = 10000
)

type benchmarkSplitRow struct {
	Index       int
	ID          string
	Values      map[string]string
	InputKey    string
	Target      string
	Group       string
	TimeValue   float64
	HasTime     bool
	Stratum     string
	Split       string
	InputTokens map[string]struct{}
}

type benchmarkSplitUnit struct {
	Key       string
	Rows      []*benchmarkSplitRow
	Stratum   string
	TimeValue float64
	Split     string
}

func (a *BenchmarkAgent) executeDatasetAudit(ctx context.Context, task *models.Task) error {
	manifest, err := profileBenchmarkDataset(task)
	if err != nil {
		return failBenchmarkTask(task, fmt.Errorf("benchmark dataset audit failed: %w", err))
	}
	files, err := benchmarkUploadsFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	primary, ok := selectBenchmarkDatasetFile(files)
	if !ok {
		return failBenchmarkTask(task, fmt.Errorf("benchmark dataset is unavailable"))
	}
	rows, _, err := readBenchmarkRows(primary)
	if err != nil {
		return failBenchmarkTask(task, err)
	}

	columnNames := make([]string, 0, len(manifest.Columns))
	for _, column := range manifest.Columns {
		columnNames = append(columnNames, column.Name)
	}
	inputColumns := benchmarkRequestedInputColumns(task, manifest.InputColumn, columnNames)
	taskType, confidence, reasons, err := benchmarkTaskType(task, manifest, rows)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	manifest.SuggestedTask = taskType
	manifest.InputColumn = inputColumns[0]
	groupColumn, err := benchmarkOptionalColumn(task, "benchmark_group_column", columnNames)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	timeColumn, err := benchmarkOptionalColumn(task, "benchmark_time_column", columnNames)
	if err != nil {
		return failBenchmarkTask(task, err)
	}

	missing := make(map[string]int, len(inputColumns)+3)
	tracked := append([]string(nil), inputColumns...)
	for _, name := range []string{manifest.TargetColumn, groupColumn, timeColumn} {
		if name != "" {
			tracked = append(tracked, name)
		}
	}
	classCounts := map[string]int{}
	for _, row := range rows {
		for _, column := range tracked {
			if strings.TrimSpace(row[column]) == "" {
				missing[column]++
			}
		}
		if manifest.TargetColumn != "" && strings.TrimSpace(row[manifest.TargetColumn]) != "" {
			classCounts[row[manifest.TargetColumn]]++
		}
	}
	if taskType != "classification" {
		classCounts = nil
	}

	issues := make([]string, 0, 4)
	if manifest.RequiresConfirmation {
		issues = append(issues, "input or target column mapping is ambiguous")
	}
	if benchmarkTaskRequiresTarget(taskType) && manifest.TargetColumn == "" {
		issues = append(issues, taskType+" requires an explicit target column")
	}
	if taskType == "classification" && len(classCounts) < 2 {
		issues = append(issues, "classification requires at least two target classes")
	}
	if len(inputColumns) == 0 || inputColumns[0] == "" {
		issues = append(issues, "at least one input column is required")
	}
	audit := models.BenchmarkDatasetAudit{
		Version: models.BenchmarkAuditVersion, DatasetSHA256: manifest.SHA256,
		TaskType: taskType, InputColumns: inputColumns, TargetColumn: manifest.TargetColumn,
		GroupColumn: groupColumn, TimeColumn: timeColumn, Confidence: confidence,
		Reasons: reasons, ClassCounts: classCounts, MissingCounts: missing,
		BlockingIssues: issues, Metadata: map[string]string{
			"profile_scope": fmt.Sprintf("first_%d_rows_max", benchmarkDatasetSampleLimit),
			"source_name":   manifest.Name,
		}, CreatedAt: time.Now().UTC(),
	}
	manifestJSON, _ := json.Marshal(manifest)
	auditJSON, _ := json.Marshal(audit)
	task.Result, task.StructuredData, task.Status = string(auditJSON), string(auditJSON), models.StatusCompleted
	setBenchmarkArtifacts(task, map[string]string{
		"dataset_manifest":        string(manifestJSON),
		"benchmark_dataset_audit": string(auditJSON),
	})
	logToContext(ctx, "[%s] audited %s as %s with confidence %.2f", a.Name, manifest.Name, taskType, confidence)
	return nil
}

func benchmarkRequestedInputColumns(task *models.Task, fallback string, available []string) []string {
	raw := benchmarkTaskString(task, "benchmark_input_columns")
	if raw == "" {
		raw = benchmarkTaskString(task, "benchmark_input_column")
	}
	values := make([]string, 0, 4)
	if raw != "" {
		for _, part := range strings.Split(raw, ",") {
			candidate := strings.TrimSpace(part)
			for _, column := range available {
				if strings.EqualFold(column, candidate) {
					values = append(values, column)
					break
				}
			}
		}
	}
	if len(values) == 0 && fallback != "" {
		values = append(values, fallback)
	}
	return cleanBenchmarkStrings(values, 16)
}

func benchmarkOptionalColumn(task *models.Task, key string, available []string) (string, error) {
	candidate := benchmarkTaskString(task, key)
	if candidate == "" {
		return "", nil
	}
	for _, column := range available {
		if strings.EqualFold(column, candidate) {
			return column, nil
		}
	}
	return "", fmt.Errorf("%s %q does not exist in the dataset", key, candidate)
}

func benchmarkTaskType(task *models.Task, manifest models.DatasetManifest, rows []map[string]string) (string, float64, []string, error) {
	explicit := strings.ToLower(benchmarkTaskString(task, "benchmark_task_type"))
	aliases := map[string]string{
		"classification": "classification", "分类": "classification",
		"regression": "regression", "回归": "regression",
		"generation": "generation", "生成": "generation",
		"inference": "inference", "推理": "inference",
		"retrieval": "retrieval", "检索": "retrieval",
	}
	if explicit != "" {
		taskType := aliases[explicit]
		if taskType == "" {
			return "", 0, nil, fmt.Errorf("unsupported benchmark_task_type %q", explicit)
		}
		return taskType, 1, []string{"task type explicitly provided by the user"}, nil
	}
	taskType := manifest.SuggestedTask
	reasons := []string{"task type inferred from input and target column values"}
	confidence := manifest.MappingConfidence * 0.9
	if taskType == "classification" && manifest.TargetColumn != "" {
		unique := map[string]struct{}{}
		totalLength := 0
		observed := 0
		numeric := true
		for _, row := range rows {
			value := strings.TrimSpace(row[manifest.TargetColumn])
			if value == "" {
				continue
			}
			unique[value] = struct{}{}
			totalLength += len([]rune(value))
			observed++
			if _, err := strconv.ParseFloat(value, 64); err != nil {
				numeric = false
			}
		}
		if !numeric && observed >= 20 && len(unique)*2 > observed && totalLength/observed >= 16 {
			taskType = "generation"
			confidence = math.Min(confidence, 0.72)
			reasons = []string{"high-cardinality textual targets suggest a generation task"}
		}
	}
	if taskType == "unknown" {
		return "", 0, nil, fmt.Errorf("unable to infer benchmark task; provide benchmark_task_type and column names")
	}
	return taskType, confidence, reasons, nil
}

func benchmarkTaskRequiresTarget(taskType string) bool {
	return taskType == "classification" || taskType == "regression" || taskType == "generation" || taskType == "retrieval"
}

func (a *BenchmarkAgent) executeSplitMaterialization(ctx context.Context, task *models.Task) error {
	workspace, err := benchmarkWorkspace(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	manifest, _, err := benchmarkDatasetManifestFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	audit, err := benchmarkAuditFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if len(audit.BlockingIssues) > 0 {
		return failBenchmarkTask(task, fmt.Errorf("benchmark audit requires clarification: %s", strings.Join(audit.BlockingIssues, "; ")))
	}
	if audit.DatasetSHA256 != manifest.SHA256 {
		return failBenchmarkTask(task, fmt.Errorf("benchmark audit does not match dataset manifest"))
	}
	files, err := benchmarkUploadsFromTask(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	primary, ok := selectBenchmarkDatasetFile(files)
	if !ok {
		return failBenchmarkTask(task, fmt.Errorf("uploaded benchmark dataset is unavailable"))
	}
	if actual, hashErr := sha256File(primary.StoragePath); hashErr != nil || actual != manifest.SHA256 {
		return failBenchmarkTask(task, fmt.Errorf("uploaded benchmark dataset changed after audit"))
	}
	rawRows, err := readAllBenchmarkRows(primary, benchmarkMaxSplitRows)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	seed := boundedTaskInt(task, "benchmark_split_seed", 17, 0, math.MaxInt32)
	ratios, err := benchmarkSplitRatios(task)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	rows, excluded, conflicting, err := prepareBenchmarkSplitRows(rawRows, manifest.SHA256, audit)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if len(rows) < 6 {
		return failBenchmarkTask(task, fmt.Errorf("benchmark needs at least 6 eligible rows for isolated train/validation/test splits; got %d", len(rows)))
	}
	method := benchmarkSplitMethod(audit)
	units, duplicateGroups, err := buildBenchmarkSplitUnits(rows, audit, method, seed)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	if len(units) < 3 {
		return failBenchmarkTask(task, fmt.Errorf("benchmark needs at least 3 independent groups after duplicate isolation; got %d", len(units)))
	}
	assignBenchmarkSplitUnits(units, method, ratios, seed)
	for _, unit := range units {
		for _, row := range unit.Rows {
			row.Split = unit.Split
		}
	}
	if err := ensureBenchmarkSplitsNonEmpty(rows); err != nil {
		return failBenchmarkTask(task, err)
	}
	leakage := inspectBenchmarkLeakage(rows, audit, duplicateGroups, conflicting, method)
	if leakage.Status != "passed" {
		payload, _ := json.Marshal(leakage)
		task.Result = string(payload)
		return failBenchmarkTask(task, fmt.Errorf("benchmark split failed leakage guard"))
	}

	splitManifest, validationManifest, testManifest, preflightManifest, err := materializeBenchmarkSplits(workspace, manifest, audit, rows, excluded, method, seed, ratios)
	if err != nil {
		return failBenchmarkTask(task, err)
	}
	splitJSON, _ := json.Marshal(splitManifest)
	leakageJSON, _ := json.Marshal(leakage)
	validationJSON, _ := json.Marshal(validationManifest)
	testJSON, _ := json.Marshal(testManifest)
	preflightJSON, _ := json.Marshal(preflightManifest)
	task.Result, task.StructuredData, task.Status = string(splitJSON), string(splitJSON), models.StatusCompleted
	setBenchmarkArtifacts(task, map[string]string{
		"benchmark_split_manifest":                string(splitJSON),
		"benchmark_leakage_report":                string(leakageJSON),
		"benchmark_validation_dataset_manifest":   string(validationJSON),
		"benchmark_public_test_manifest":          string(testJSON),
		"benchmark_input_only_preflight_manifest": string(preflightJSON),
		"benchmark_train_path":                    splitManifest.Splits["train"].RelativePath,
		"benchmark_validation_path":               splitManifest.Splits["validation"].RelativePath,
		"benchmark_test_features_path":            splitManifest.Splits["test"].RelativePath,
	})
	logToContext(ctx, "[%s] materialized %s split train=%d validation=%d test=%d", a.Name, method,
		splitManifest.Splits["train"].RowCount, splitManifest.Splits["validation"].RowCount, splitManifest.Splits["test"].RowCount)
	return nil
}

func benchmarkAuditFromTask(task *models.Task) (models.BenchmarkDatasetAudit, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "benchmark_dataset_audit"))
	if raw == "" {
		return models.BenchmarkDatasetAudit{}, fmt.Errorf("benchmark_dataset_audit input is required")
	}
	var audit models.BenchmarkDatasetAudit
	if err := json.Unmarshal([]byte(raw), &audit); err != nil {
		return audit, fmt.Errorf("decode benchmark dataset audit: %w", err)
	}
	if audit.Version != models.BenchmarkAuditVersion || audit.DatasetSHA256 == "" || audit.TaskType == "" || len(audit.InputColumns) == 0 {
		return audit, fmt.Errorf("benchmark dataset audit is incomplete")
	}
	return audit, nil
}

func benchmarkSplitRatios(task *models.Task) (map[string]float64, error) {
	values := map[string]float64{"train": 0.70, "validation": 0.15, "test": 0.15}
	for name, key := range map[string]string{
		"train": "benchmark_train_ratio", "validation": "benchmark_validation_ratio", "test": "benchmark_test_ratio",
	} {
		if raw := benchmarkTaskString(task, key); raw != "" {
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil || !isFiniteBenchmarkNumber(value) || value <= 0 || value >= 1 {
				return nil, fmt.Errorf("%s must be between 0 and 1", key)
			}
			values[name] = value
		}
	}
	total := values["train"] + values["validation"] + values["test"]
	if math.Abs(total-1) > 1e-9 {
		return nil, fmt.Errorf("benchmark split ratios must sum to 1; got %.6f", total)
	}
	return values, nil
}

func benchmarkSplitMethod(audit models.BenchmarkDatasetAudit) string {
	if audit.TimeColumn != "" {
		return "chronological"
	}
	if audit.GroupColumn != "" {
		return "group_hash"
	}
	switch audit.TaskType {
	case "classification":
		return "stratified_hash"
	case "regression":
		return "quantile_stratified_hash"
	default:
		return "deterministic_hash"
	}
}

func prepareBenchmarkSplitRows(rawRows []map[string]string, sourceSHA string, audit models.BenchmarkDatasetAudit) ([]*benchmarkSplitRow, int, int, error) {
	rows := make([]*benchmarkSplitRow, 0, len(rawRows))
	excluded := 0
	inputTargets := map[string]map[string]struct{}{}
	for index, values := range rawRows {
		inputKey := normalizedBenchmarkInput(values, audit.InputColumns)
		target := strings.TrimSpace(values[audit.TargetColumn])
		if inputKey == "" || (benchmarkTaskRequiresTarget(audit.TaskType) && target == "") {
			excluded++
			continue
		}
		canonical, _ := json.Marshal(values)
		identity := sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", sourceSHA, index, canonical)))
		row := &benchmarkSplitRow{
			Index: index, ID: hex.EncodeToString(identity[:10]), Values: values,
			InputKey: inputKey, Target: target, Group: strings.TrimSpace(values[audit.GroupColumn]),
			InputTokens: benchmarkTokenSet(inputKey),
		}
		if audit.TimeColumn != "" {
			parsed, err := parseBenchmarkTime(values[audit.TimeColumn])
			if err != nil {
				return nil, excluded, 0, fmt.Errorf("row %d time column %s: %w", index+1, audit.TimeColumn, err)
			}
			row.TimeValue, row.HasTime = parsed, true
		}
		if audit.TargetColumn != "" {
			if inputTargets[inputKey] == nil {
				inputTargets[inputKey] = map[string]struct{}{}
			}
			inputTargets[inputKey][target] = struct{}{}
		}
		rows = append(rows, row)
	}
	conflicting := 0
	for _, targets := range inputTargets {
		if len(targets) > 1 {
			conflicting++
		}
	}
	if conflicting > 0 {
		return nil, excluded, conflicting, fmt.Errorf("dataset contains %d normalized inputs with conflicting targets", conflicting)
	}
	if audit.TaskType == "classification" {
		classes := map[string]int{}
		for _, row := range rows {
			classes[row.Target]++
		}
		if len(classes) < 2 {
			return nil, excluded, conflicting, fmt.Errorf("classification split has fewer than two eligible classes")
		}
	}
	return rows, excluded, conflicting, nil
}

func normalizedBenchmarkInput(values map[string]string, columns []string) string {
	parts := make([]string, 0, len(columns))
	for _, column := range columns {
		var builder strings.Builder
		for _, r := range strings.ToLower(strings.TrimSpace(values[column])) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				builder.WriteRune(r)
			} else {
				builder.WriteRune(' ')
			}
		}
		parts = append(parts, strings.Join(strings.Fields(builder.String()), " "))
	}
	return strings.TrimSpace(strings.Join(parts, " | "))
}

func benchmarkTokenSet(value string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, token := range strings.Fields(value) {
		result[token] = struct{}{}
	}
	return result
}

func parseBenchmarkTime(raw string) (float64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, fmt.Errorf("value is empty")
	}
	if numeric, err := strconv.ParseFloat(value, 64); err == nil && isFiniteBenchmarkNumber(numeric) {
		return numeric, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return float64(parsed.UnixNano()) / float64(time.Second), nil
		}
	}
	return 0, fmt.Errorf("value %q is not a supported timestamp", value)
}

func buildBenchmarkSplitUnits(rows []*benchmarkSplitRow, audit models.BenchmarkDatasetAudit, method string, seed int) ([]*benchmarkSplitUnit, int, error) {
	grouped := map[string][]*benchmarkSplitRow{}
	inputCounts := map[string]int{}
	for _, row := range rows {
		inputCounts[row.InputKey]++
		key := row.InputKey
		if method == "group_hash" || method == "chronological" && audit.GroupColumn != "" {
			if row.Group == "" {
				key = "missing-group:" + row.ID
			} else {
				key = "group:" + row.Group
			}
		}
		grouped[key] = append(grouped[key], row)
	}
	duplicateGroups := 0
	for _, count := range inputCounts {
		if count > 1 {
			duplicateGroups++
		}
	}
	units := make([]*benchmarkSplitUnit, 0, len(grouped))
	for key, groupedRows := range grouped {
		unit := &benchmarkSplitUnit{Key: key, Rows: groupedRows}
		if method == "chronological" {
			unit.TimeValue = groupedRows[0].TimeValue
			for _, row := range groupedRows[1:] {
				unit.TimeValue = math.Min(unit.TimeValue, row.TimeValue)
			}
		}
		if audit.TaskType == "classification" {
			counts := map[string]int{}
			for _, row := range groupedRows {
				counts[row.Target]++
			}
			labels := make([]string, 0, len(counts))
			for label := range counts {
				labels = append(labels, label)
			}
			sort.Slice(labels, func(i, j int) bool {
				if counts[labels[i]] == counts[labels[j]] {
					return labels[i] < labels[j]
				}
				return counts[labels[i]] > counts[labels[j]]
			})
			unit.Stratum = labels[0]
		}
		units = append(units, unit)
	}
	if audit.TaskType == "regression" && method == "quantile_stratified_hash" {
		sort.Slice(units, func(i, j int) bool {
			return benchmarkUnitMeanTarget(units[i]) < benchmarkUnitMeanTarget(units[j])
		})
		for index, unit := range units {
			unit.Stratum = fmt.Sprintf("q%d", min(4, index*5/len(units)))
		}
	}
	for _, unit := range units {
		if unit.Stratum == "" {
			unit.Stratum = "all"
		}
	}
	sort.Slice(units, func(i, j int) bool {
		return benchmarkStableUnitKey(units[i].Key, seed) < benchmarkStableUnitKey(units[j].Key, seed)
	})
	return units, duplicateGroups, nil
}

func benchmarkUnitMeanTarget(unit *benchmarkSplitUnit) float64 {
	if unit == nil || len(unit.Rows) == 0 {
		return 0
	}
	total := 0.0
	for _, row := range unit.Rows {
		value, _ := strconv.ParseFloat(row.Target, 64)
		total += value
	}
	return total / float64(len(unit.Rows))
}

func benchmarkStableUnitKey(value string, seed int) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", seed, value)))
	return hex.EncodeToString(hash[:])
}

func assignBenchmarkSplitUnits(units []*benchmarkSplitUnit, method string, ratios map[string]float64, seed int) {
	if method == "chronological" {
		sort.SliceStable(units, func(i, j int) bool {
			if units[i].TimeValue == units[j].TimeValue {
				return benchmarkStableUnitKey(units[i].Key, seed) < benchmarkStableUnitKey(units[j].Key, seed)
			}
			return units[i].TimeValue < units[j].TimeValue
		})
		assignBenchmarkUnitSlice(units, ratios)
		return
	}
	strata := map[string][]*benchmarkSplitUnit{}
	for _, unit := range units {
		strata[unit.Stratum] = append(strata[unit.Stratum], unit)
	}
	keys := make([]string, 0, len(strata))
	for key := range strata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		sort.Slice(strata[key], func(i, j int) bool {
			return benchmarkStableUnitKey(strata[key][i].Key, seed) < benchmarkStableUnitKey(strata[key][j].Key, seed)
		})
		assignBenchmarkUnitSlice(strata[key], ratios)
	}
	rebalanceBenchmarkUnitSplits(units)
}

func assignBenchmarkUnitSlice(units []*benchmarkSplitUnit, ratios map[string]float64) {
	train, validation, _ := benchmarkThreeWayCounts(len(units), ratios)
	for index, unit := range units {
		switch {
		case index < train:
			unit.Split = "train"
		case index < train+validation:
			unit.Split = "validation"
		default:
			unit.Split = "test"
		}
	}
}

func benchmarkThreeWayCounts(total int, ratios map[string]float64) (int, int, int) {
	if total <= 0 {
		return 0, 0, 0
	}
	if total < 3 {
		return total, 0, 0
	}
	validation := max(1, int(math.Round(float64(total)*ratios["validation"])))
	test := max(1, int(math.Round(float64(total)*ratios["test"])))
	train := total - validation - test
	for train < 1 {
		if validation >= test && validation > 1 {
			validation--
		} else if test > 1 {
			test--
		} else {
			break
		}
		train = total - validation - test
	}
	return train, validation, test
}

func rebalanceBenchmarkUnitSplits(units []*benchmarkSplitUnit) {
	counts := map[string]int{}
	for _, unit := range units {
		counts[unit.Split]++
	}
	for _, missing := range []string{"validation", "test"} {
		if counts[missing] > 0 {
			continue
		}
		for index := len(units) - 1; index >= 0; index-- {
			if counts[units[index].Split] > 1 {
				counts[units[index].Split]--
				units[index].Split = missing
				counts[missing]++
				break
			}
		}
	}
}

func ensureBenchmarkSplitsNonEmpty(rows []*benchmarkSplitRow) error {
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Split]++
	}
	for _, name := range []string{"train", "validation", "test"} {
		if counts[name] == 0 {
			return fmt.Errorf("benchmark %s split is empty after grouping", name)
		}
	}
	return nil
}

func inspectBenchmarkLeakage(rows []*benchmarkSplitRow, audit models.BenchmarkDatasetAudit, duplicateGroups, conflicting int, method string) models.BenchmarkLeakageReport {
	report := models.BenchmarkLeakageReport{
		Version: "benchmark.leakage/v1", Status: "passed", ExactDuplicateGroups: duplicateGroups,
		ConflictingTargetGroups: conflicting, Checks: []string{
			"normalized input fingerprints do not cross splits",
			"explicit groups do not cross splits",
			"time splits preserve chronological order when configured",
			"bounded token-Jaccard near-duplicate scan completed",
		},
	}
	inputSplits := map[string]map[string]struct{}{}
	groupSplits := map[string]map[string]struct{}{}
	bySplit := map[string][]*benchmarkSplitRow{}
	for _, row := range rows {
		if inputSplits[row.InputKey] == nil {
			inputSplits[row.InputKey] = map[string]struct{}{}
		}
		inputSplits[row.InputKey][row.Split] = struct{}{}
		if audit.GroupColumn != "" && row.Group != "" {
			if groupSplits[row.Group] == nil {
				groupSplits[row.Group] = map[string]struct{}{}
			}
			groupSplits[row.Group][row.Split] = struct{}{}
		}
		bySplit[row.Split] = append(bySplit[row.Split], row)
	}
	for _, splits := range inputSplits {
		if len(splits) > 1 {
			report.CrossSplitInputOverlaps++
		}
	}
	for _, splits := range groupSplits {
		if len(splits) > 1 {
			report.CrossSplitGroupOverlaps++
		}
	}
	if method == "chronological" {
		maxTrain, minValidation, maxValidation, minTest := -math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64
		for _, row := range rows {
			switch row.Split {
			case "train":
				maxTrain = math.Max(maxTrain, row.TimeValue)
			case "validation":
				minValidation, maxValidation = math.Min(minValidation, row.TimeValue), math.Max(maxValidation, row.TimeValue)
			case "test":
				minTest = math.Min(minTest, row.TimeValue)
			}
		}
		if maxTrain > minValidation {
			report.ChronologyViolations++
		}
		if maxValidation > minTest {
			report.ChronologyViolations++
		}
	}
	report.NearDuplicatePairsChecked, report.NearDuplicateCrossSplit = benchmarkNearDuplicateScan(bySplit, benchmarkNearPairLimit)
	if report.NearDuplicateCrossSplit > 0 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("found %d high-overlap token pairs across splits; inspect domain-specific leakage", report.NearDuplicateCrossSplit))
	}
	if report.ConflictingTargetGroups > 0 || report.CrossSplitInputOverlaps > 0 || report.CrossSplitGroupOverlaps > 0 || report.ChronologyViolations > 0 {
		report.Status = "failed"
	}
	return report
}

func benchmarkNearDuplicateScan(bySplit map[string][]*benchmarkSplitRow, limit int) (int, int) {
	pairs := [][2]string{{"train", "validation"}, {"train", "test"}, {"validation", "test"}}
	checked, matches := 0, 0
	for _, pair := range pairs {
		for _, left := range bySplit[pair[0]] {
			for _, right := range bySplit[pair[1]] {
				if checked >= limit {
					return checked, matches
				}
				checked++
				if left.InputKey == right.InputKey {
					continue
				}
				if benchmarkJaccard(left.InputTokens, right.InputTokens) >= 0.90 {
					matches++
				}
			}
		}
	}
	return checked, matches
}

func benchmarkJaccard(left, right map[string]struct{}) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	for token := range left {
		if _, ok := right[token]; ok {
			intersection++
		}
	}
	return float64(intersection) / float64(len(left)+len(right)-intersection)
}

func materializeBenchmarkSplits(
	workspace string,
	source models.DatasetManifest,
	audit models.BenchmarkDatasetAudit,
	rows []*benchmarkSplitRow,
	excluded int,
	method string,
	seed int,
	ratios map[string]float64,
) (models.BenchmarkSplitManifest, models.DatasetManifest, models.DatasetManifest, models.DatasetManifest, error) {
	directory, err := benchmarkPathInWorkspace(workspace, benchmarkSplitDirectory)
	if err != nil {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
	}
	if err := os.RemoveAll(directory); err != nil {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
	}
	specIdentity, _ := json.Marshal(map[string]any{
		"dataset": source.SHA256, "task": audit.TaskType, "method": method, "seed": seed,
		"ratios": ratios, "inputs": audit.InputColumns, "target": audit.TargetColumn,
		"group": audit.GroupColumn, "time": audit.TimeColumn,
	})
	idHash := sha256.Sum256(specIdentity)
	splitID := "split-" + hex.EncodeToString(idHash[:10])
	privateStateID := splitID
	privateDirectory, err := benchmarkPrivateStateDirectory(privateStateID, true)
	if err != nil {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
	}
	if benchmarkFilesystemContains(workspace, privateDirectory) || benchmarkFilesystemContains(privateDirectory, workspace) {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, fmt.Errorf("benchmark private state must be outside the repository workspace")
	}

	artifacts := map[string]models.BenchmarkSplitArtifact{}
	for _, split := range []string{"train", "validation", "test"} {
		selected := make([]*benchmarkSplitRow, 0)
		for _, row := range rows {
			if row.Split == split {
				selected = append(selected, row)
			}
		}
		sort.Slice(selected, func(i, j int) bool { return selected[i].Index < selected[j].Index })
		filename := split + ".jsonl"
		if split == "test" {
			filename = "test_features.jsonl"
		}
		path := filepath.Join(directory, filename)
		if err := writeBenchmarkRowsJSONL(path, selected, audit.TargetColumn, split == "test"); err != nil {
			return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
		}
		sha, err := sha256File(path)
		if err != nil {
			return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
		}
		relative, _ := filepath.Rel(workspace, path)
		artifacts[split] = models.BenchmarkSplitArtifact{
			Name: split, RelativePath: filepath.ToSlash(relative), SHA256: sha,
			RowCount: len(selected), TargetPublic: split != "test",
			Distribution: benchmarkSplitDistribution(selected, audit.TaskType),
		}
	}
	validationRows := make([]*benchmarkSplitRow, 0, artifacts["validation"].RowCount)
	for _, row := range rows {
		if row.Split == "validation" {
			validationRows = append(validationRows, row)
		}
	}
	sort.Slice(validationRows, func(i, j int) bool { return validationRows[i].Index < validationRows[j].Index })
	if len(validationRows) > 8 {
		validationRows = validationRows[:8]
	}
	preflightPath := filepath.Join(directory, "preflight_features.jsonl")
	if err := writeBenchmarkRowsJSONL(preflightPath, validationRows, audit.TargetColumn, true); err != nil {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
	}
	preflightHash, err := sha256File(preflightPath)
	if err != nil {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
	}
	preflightRelative, _ := filepath.Rel(workspace, preflightPath)
	preflightArtifact := models.BenchmarkSplitArtifact{
		Name: "preflight_features", RelativePath: filepath.ToSlash(preflightRelative), SHA256: preflightHash,
		RowCount: len(validationRows), TargetPublic: false,
	}
	hiddenLabelsHash := ""
	if benchmarkTaskRequiresTarget(audit.TaskType) {
		testRows := make([]*benchmarkSplitRow, 0, artifacts["test"].RowCount)
		for _, row := range rows {
			if row.Split == "test" {
				testRows = append(testRows, row)
			}
		}
		sort.Slice(testRows, func(i, j int) bool { return testRows[i].Index < testRows[j].Index })
		labelsPath := filepath.Join(privateDirectory, "test_labels.jsonl")
		if err := writeBenchmarkHiddenLabels(labelsPath, testRows); err != nil {
			return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
		}
		hiddenLabelsHash, err = sha256File(labelsPath)
		if err != nil {
			return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
		}
	} else {
		privateStateID = ""
	}
	manifest := models.BenchmarkSplitManifest{
		Version: models.BenchmarkSplitVersion, ID: splitID, DatasetSHA256: source.SHA256,
		TaskType: audit.TaskType, Method: method, Seed: seed, Ratios: ratios,
		InputColumns: append([]string(nil), audit.InputColumns...), TargetColumn: audit.TargetColumn,
		GroupColumn: audit.GroupColumn, TimeColumn: audit.TimeColumn, Splits: artifacts,
		PreflightFeatures: &preflightArtifact,
		PrivateStateID:    privateStateID, HiddenLabelsSHA: hiddenLabelsHash,
		SourceRowCount: source.RowCount, EligibleRowCount: len(rows), ExcludedRowCount: excluded,
		MaterializedAt: time.Now().UTC(),
	}
	manifestJSON, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "split_manifest.json"), manifestJSON, 0o600); err != nil {
		return models.BenchmarkSplitManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, models.DatasetManifest{}, err
	}
	validation := splitDatasetManifest(source, audit, artifacts["validation"], "validation.jsonl", audit.TargetColumn)
	test := splitDatasetManifest(source, audit, artifacts["test"], "test_features.jsonl", "")
	preflight := splitDatasetManifest(source, audit, preflightArtifact, "preflight_features.jsonl", "")
	return manifest, validation, test, preflight, nil
}

func splitDatasetManifest(source models.DatasetManifest, audit models.BenchmarkDatasetAudit, artifact models.BenchmarkSplitArtifact, name, target string) models.DatasetManifest {
	result := source
	result.Version = "benchmark.dataset/v1"
	result.Name = name
	result.Format = "jsonl"
	result.RelativePath = artifact.RelativePath
	result.SHA256 = artifact.SHA256
	result.RowCount = artifact.RowCount
	result.Size = 0
	result.InputColumn = audit.InputColumns[0]
	result.TargetColumn = target
	result.SuggestedTask = audit.TaskType
	result.MappingConfidence = 1
	result.RequiresConfirmation = false
	result.SamplePreview = nil
	return result
}

func writeBenchmarkRowsJSONL(path string, rows []*benchmarkSplitRow, targetColumn string, hideTarget bool) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		value := make(map[string]string, len(row.Values)+1)
		for key, item := range row.Values {
			if hideTarget && key == targetColumn {
				continue
			}
			value[key] = item
		}
		value["__benchmark_id"] = row.ID
		if err := encoder.Encode(value); err != nil {
			_ = file.Close()
			return err
		}
	}
	return file.Close()
}

func writeBenchmarkHiddenLabels(path string, rows []*benchmarkSplitRow) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	for _, row := range rows {
		if err := encoder.Encode(map[string]string{"id": row.ID, "target": row.Target}); err != nil {
			_ = file.Close()
			return err
		}
	}
	return file.Close()
}

func benchmarkSplitDistribution(rows []*benchmarkSplitRow, taskType string) map[string]int {
	if taskType != "classification" {
		return nil
	}
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Target]++
	}
	return counts
}

func benchmarkPrivateStateDirectory(stateID string, reset bool) (string, error) {
	if stateID == "" || strings.ContainsAny(stateID, `/\\`) || strings.Contains(stateID, "..") {
		return "", fmt.Errorf("benchmark private state id is invalid")
	}
	root := strings.TrimSpace(os.Getenv("BENCHMARK_PRIVATE_ROOT"))
	if root == "" {
		cacheRoot, cacheErr := os.UserCacheDir()
		if cacheErr != nil || strings.TrimSpace(cacheRoot) == "" {
			return "", fmt.Errorf("BENCHMARK_PRIVATE_ROOT is required when the user cache directory is unavailable")
		}
		root = filepath.Join(cacheRoot, "scholar-agent", "private-benchmarks")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	workspaceRoots := strings.TrimSpace(os.Getenv("SANDBOX_WORKSPACE_ROOTS"))
	if workspaceRoots == "" {
		workspaceRoots = os.TempDir()
	}
	for _, workspaceRoot := range filepath.SplitList(workspaceRoots) {
		workspaceRoot = strings.TrimSpace(workspaceRoot)
		if workspaceRoot == "" {
			continue
		}
		workspaceRoot, err = filepath.Abs(workspaceRoot)
		if err != nil {
			return "", fmt.Errorf("resolve sandbox workspace root: %w", err)
		}
		if benchmarkFilesystemContains(workspaceRoot, root) || benchmarkFilesystemContains(root, workspaceRoot) {
			return "", fmt.Errorf("benchmark private root must be isolated from SANDBOX_WORKSPACE_ROOTS")
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if info, err := os.Lstat(root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("benchmark private root is unsafe")
	}
	directory := filepath.Join(root, stateID)
	if reset {
		if err := os.RemoveAll(directory); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("benchmark private state directory is unsafe")
	}
	return directory, nil
}

func benchmarkFilesystemContains(parent, child string) bool {
	parentAbs, parentErr := filepath.Abs(parent)
	childAbs, childErr := filepath.Abs(child)
	if parentErr != nil || childErr != nil {
		return false
	}
	relative, err := filepath.Rel(parentAbs, childAbs)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readAllBenchmarkRows(file benchmarkUploadedFile, limit int) ([]map[string]string, error) {
	switch strings.ToLower(filepath.Ext(file.Name)) {
	case ".csv":
		return readAllDelimitedBenchmarkRows(file.StoragePath, ',', limit)
	case ".tsv":
		return readAllDelimitedBenchmarkRows(file.StoragePath, '\t', limit)
	case ".json":
		return readAllJSONBenchmarkRows(file.StoragePath, limit)
	case ".jsonl":
		return readAllJSONLBenchmarkRows(file.StoragePath, limit)
	default:
		return nil, fmt.Errorf("unsupported dataset format")
	}
}

func readAllDelimitedBenchmarkRows(path string, delimiter rune, limit int) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.Comma, reader.FieldsPerRecord = delimiter, -1
	header, err := reader.Read()
	if err != nil {
		return nil, err
	}
	header = normalizeBenchmarkHeader(header)
	rows := make([]map[string]string, 0, min(limit, 1024))
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		if len(rows) >= limit {
			return nil, fmt.Errorf("dataset exceeds the %d-row benchmark split limit", limit)
		}
		row := make(map[string]string, len(header))
		for index, name := range header {
			if index < len(record) {
				row[name] = record[index]
			} else {
				row[name] = ""
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readAllJSONBenchmarkRows(path string, limit int) ([]map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	values, ok := payload.([]any)
	if !ok {
		if object, objectOK := payload.(map[string]any); objectOK {
			for _, key := range []string{"data", "records", "items", "examples"} {
				if candidate, candidateOK := object[key].([]any); candidateOK {
					values, ok = candidate, true
					break
				}
			}
		}
	}
	if !ok {
		return nil, fmt.Errorf("JSON dataset must be an array of records")
	}
	if len(values) > limit {
		return nil, fmt.Errorf("dataset exceeds the %d-row benchmark split limit", limit)
	}
	rows := make([]map[string]string, 0, len(values))
	for index, value := range values {
		object, objectOK := value.(map[string]any)
		if !objectOK {
			return nil, fmt.Errorf("JSON record %d is not an object", index+1)
		}
		rows = append(rows, stringifyBenchmarkRow(object))
	}
	return rows, nil
}

func readAllJSONLBenchmarkRows(path string, limit int) ([]map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	rows := make([]map[string]string, 0, min(limit, 1024))
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		if len(rows) >= limit {
			return nil, fmt.Errorf("dataset exceeds the %d-row benchmark split limit", limit)
		}
		var object map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &object); err != nil {
			return nil, fmt.Errorf("decode JSONL record %d: %w", len(rows)+1, err)
		}
		rows = append(rows, stringifyBenchmarkRow(object))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func isFiniteBenchmarkNumber(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
