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
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"scholar-agent-backend/internal/models"
)

const benchmarkDatasetSampleLimit = 1000

type benchmarkUploadedFile struct {
	Name        string `json:"name"`
	StoragePath string `json:"storage_path"`
	SHA256      string `json:"sha256"`
	Size        int64  `json:"size"`
}

func (a *ResearchCodingAgent) executeDatasetProfile(ctx context.Context, task *models.Task) error {
	manifest, err := profileBenchmarkDataset(task)
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("dataset profile failed: %w", err))
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return failResearchCodingTask(task, fmt.Errorf("encode dataset manifest: %w", err))
	}
	task.Result = string(payload)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"dataset_manifest": string(payload),
	})
	logToContext(ctx, "[%s] dataset profile: %s rows=%d input=%s target=%s", a.Name, manifest.Name, manifest.RowCount, manifest.InputColumn, manifest.TargetColumn)
	return nil
}

func profileBenchmarkDataset(task *models.Task) (models.DatasetManifest, error) {
	files, err := benchmarkUploadsFromTask(task)
	if err != nil {
		return models.DatasetManifest{}, err
	}
	primary, ok := selectBenchmarkDatasetFile(files)
	if !ok {
		return models.DatasetManifest{}, fmt.Errorf("no supported dataset attachment; expected csv, tsv, json, or jsonl")
	}

	rows, rowCount, err := readBenchmarkRows(primary)
	if err != nil {
		return models.DatasetManifest{}, err
	}
	if rowCount == 0 || len(rows) == 0 {
		return models.DatasetManifest{}, fmt.Errorf("dataset %q has no records", primary.Name)
	}

	columns := profileBenchmarkColumns(rows)
	columnNames := make([]string, 0, len(columns))
	for _, column := range columns {
		columnNames = append(columnNames, column.Name)
	}
	inputHint := benchmarkTaskString(task, "benchmark_input_column")
	targetHint := benchmarkTaskString(task, "benchmark_target_column")
	if inputHint != "" && !benchmarkHasColumn(columnNames, inputHint) {
		return models.DatasetManifest{}, fmt.Errorf("input column %q does not exist in dataset", inputHint)
	}
	if targetHint != "" && !benchmarkHasColumn(columnNames, targetHint) {
		return models.DatasetManifest{}, fmt.Errorf("target column %q does not exist in dataset", targetHint)
	}
	inputColumn, inputExplicit := chooseBenchmarkColumn(columnNames, inputHint, []string{"text", "input", "prompt", "question", "sentence", "content", "review", "feature", "features"})
	targetColumn, targetExplicit := chooseBenchmarkColumn(columnNames, targetHint, []string{"label", "target", "class", "y", "answer", "score", "output"})
	if inputColumn == targetColumn {
		targetColumn = ""
		targetExplicit = false
	}

	confidence := 0.4
	if inputColumn != "" {
		confidence = 0.7
	}
	if inputExplicit {
		confidence = 0.9
	}
	if targetColumn != "" {
		confidence += 0.1
	}
	if targetExplicit {
		confidence = 1
	}
	if confidence > 1 {
		confidence = 1
	}

	sha, err := sha256File(primary.StoragePath)
	if err != nil {
		return models.DatasetManifest{}, err
	}
	if expected := strings.TrimSpace(primary.SHA256); expected != "" && !strings.EqualFold(expected, sha) {
		return models.DatasetManifest{}, fmt.Errorf("uploaded dataset checksum does not match stored metadata")
	}
	info, err := os.Stat(primary.StoragePath)
	if err != nil {
		return models.DatasetManifest{}, err
	}

	return models.DatasetManifest{
		Version:              "benchmark.dataset/v1",
		Name:                 primary.Name,
		Format:               strings.TrimPrefix(strings.ToLower(filepath.Ext(primary.Name)), "."),
		SHA256:               sha,
		Size:                 info.Size(),
		RowCount:             rowCount,
		Columns:              columns,
		InputColumn:          inputColumn,
		TargetColumn:         targetColumn,
		SuggestedTask:        inferBenchmarkTask(rows, inputColumn, targetColumn),
		MappingConfidence:    confidence,
		RequiresConfirmation: inputColumn == "" || (targetColumn == "" && !benchmarkAllowsInputOnly(task)),
		SamplePreview:        benchmarkPreview(rows, 3),
	}, nil
}

func benchmarkUploadsFromTask(task *models.Task) ([]benchmarkUploadedFile, error) {
	if task == nil || task.Inputs == nil || task.Inputs["uploaded_files"] == nil {
		return nil, fmt.Errorf("uploaded_files input is required")
	}
	raw, err := json.Marshal(task.Inputs["uploaded_files"])
	if err != nil {
		return nil, err
	}
	var files []benchmarkUploadedFile
	if err := json.Unmarshal(raw, &files); err != nil {
		return nil, err
	}
	return files, nil
}

func selectBenchmarkDatasetFile(files []benchmarkUploadedFile) (benchmarkUploadedFile, bool) {
	for _, file := range files {
		ext := strings.ToLower(filepath.Ext(file.Name))
		if ext != ".csv" && ext != ".tsv" && ext != ".json" && ext != ".jsonl" {
			continue
		}
		path := filepath.Clean(strings.TrimSpace(file.StoragePath))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file.StoragePath = path
		return file, true
	}
	return benchmarkUploadedFile{}, false
}

func readBenchmarkRows(file benchmarkUploadedFile) ([]map[string]string, int, error) {
	ext := strings.ToLower(filepath.Ext(file.Name))
	switch ext {
	case ".csv":
		return readDelimitedBenchmarkRows(file.StoragePath, ',')
	case ".tsv":
		return readDelimitedBenchmarkRows(file.StoragePath, '\t')
	case ".json":
		return readJSONBenchmarkRows(file.StoragePath)
	case ".jsonl":
		return readJSONLBenchmarkRows(file.StoragePath)
	default:
		return nil, 0, fmt.Errorf("unsupported dataset format %s", ext)
	}
}

func readDelimitedBenchmarkRows(path string, delimiter rune) ([]map[string]string, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("read dataset header: %w", err)
	}
	header = normalizeBenchmarkHeader(header)
	rows := make([]map[string]string, 0, benchmarkMinInt(benchmarkDatasetSampleLimit, 64))
	count := 0
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("read dataset row %d: %w", count+2, readErr)
		}
		count++
		if len(rows) >= benchmarkDatasetSampleLimit {
			continue
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
	return rows, count, nil
}

func readJSONBenchmarkRows(path string) ([]map[string]string, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode JSON dataset: %w", err)
	}
	values, ok := payload.([]any)
	if !ok {
		if object, objectOK := payload.(map[string]any); objectOK {
			for _, key := range []string{"data", "records", "items", "examples"} {
				if candidate, candidateOK := object[key].([]any); candidateOK {
					values = candidate
					ok = true
					break
				}
			}
		}
	}
	if !ok {
		return nil, 0, fmt.Errorf("JSON dataset must be an array of objects or contain data/records/items/examples")
	}
	rows := make([]map[string]string, 0, benchmarkMinInt(len(values), benchmarkDatasetSampleLimit))
	for index, value := range values {
		object, objectOK := value.(map[string]any)
		if !objectOK {
			return nil, 0, fmt.Errorf("JSON record %d is not an object", index+1)
		}
		if len(rows) < benchmarkDatasetSampleLimit {
			rows = append(rows, stringifyBenchmarkRow(object))
		}
	}
	return rows, len(values), nil
}

func readJSONLBenchmarkRows(path string) ([]map[string]string, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	rows := make([]map[string]string, 0, 64)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		count++
		var object map[string]any
		if err := json.Unmarshal([]byte(line), &object); err != nil {
			return nil, 0, fmt.Errorf("decode JSONL record %d: %w", count, err)
		}
		if len(rows) < benchmarkDatasetSampleLimit {
			rows = append(rows, stringifyBenchmarkRow(object))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, err
	}
	return rows, count, nil
}

func normalizeBenchmarkHeader(header []string) []string {
	seen := map[string]int{}
	result := make([]string, len(header))
	for index, raw := range header {
		name := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		if name == "" {
			name = fmt.Sprintf("column_%d", index+1)
		}
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s_%d", name, seen[name])
		}
		result[index] = name
	}
	return result
}

func stringifyBenchmarkRow(object map[string]any) map[string]string {
	row := make(map[string]string, len(object))
	for key, value := range object {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		switch typed := value.(type) {
		case nil:
			row[key] = ""
		case string:
			row[key] = typed
		case float64, bool:
			row[key] = fmt.Sprint(typed)
		default:
			raw, _ := json.Marshal(typed)
			row[key] = string(raw)
		}
	}
	return row
}

func profileBenchmarkColumns(rows []map[string]string) []models.DatasetColumnProfile {
	names := map[string]struct{}{}
	for _, row := range rows {
		for name := range row {
			names[name] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	columns := make([]models.DatasetColumnProfile, 0, len(ordered))
	for _, name := range ordered {
		nonNull := 0
		unique := map[string]struct{}{}
		values := make([]string, 0, len(rows))
		for _, row := range rows {
			value := strings.TrimSpace(row[name])
			if value == "" {
				continue
			}
			nonNull++
			unique[value] = struct{}{}
			values = append(values, value)
		}
		columns = append(columns, models.DatasetColumnProfile{
			Name: name, InferredType: inferBenchmarkColumnType(values), NonNullCount: nonNull, UniqueCount: len(unique),
		})
	}
	return columns
}

func inferBenchmarkColumnType(values []string) string {
	if len(values) == 0 {
		return "unknown"
	}
	integers := 0
	numbers := 0
	booleans := 0
	for _, value := range values {
		if _, err := strconv.ParseInt(value, 10, 64); err == nil {
			integers++
			numbers++
			continue
		}
		if _, err := strconv.ParseFloat(value, 64); err == nil {
			numbers++
			continue
		}
		if _, err := strconv.ParseBool(value); err == nil {
			booleans++
		}
	}
	if integers == len(values) {
		return "integer"
	}
	if numbers == len(values) {
		return "number"
	}
	if booleans == len(values) {
		return "boolean"
	}
	return "string"
}

func chooseBenchmarkColumn(columns []string, explicit string, candidates []string) (string, bool) {
	for _, name := range columns {
		if explicit != "" && strings.EqualFold(name, explicit) {
			return name, true
		}
	}
	for _, candidate := range candidates {
		for _, name := range columns {
			if strings.EqualFold(name, candidate) {
				return name, false
			}
		}
	}
	if len(columns) > 0 && explicit == "" {
		return columns[0], false
	}
	return "", false
}

func benchmarkHasColumn(columns []string, candidate string) bool {
	for _, name := range columns {
		if strings.EqualFold(name, strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func inferBenchmarkTask(rows []map[string]string, inputColumn, targetColumn string) string {
	if inputColumn == "" {
		return "unknown"
	}
	if targetColumn == "" {
		return "inference"
	}
	unique := map[string]struct{}{}
	numeric := true
	for _, row := range rows {
		value := strings.TrimSpace(row[targetColumn])
		if value == "" {
			continue
		}
		unique[value] = struct{}{}
		if _, err := strconv.ParseFloat(value, 64); err != nil {
			numeric = false
		}
	}
	if numeric && len(unique) > 20 {
		return "regression"
	}
	return "classification"
}

func benchmarkPreview(rows []map[string]string, limit int) []map[string]string {
	if limit > len(rows) {
		limit = len(rows)
	}
	preview := make([]map[string]string, 0, limit)
	for _, row := range rows[:limit] {
		copyRow := make(map[string]string, len(row))
		for key, value := range row {
			if len(value) > 256 {
				value = value[:256] + "..."
			}
			copyRow[key] = value
		}
		preview = append(preview, copyRow)
	}
	return preview
}

func benchmarkAllowsInputOnly(task *models.Task) bool {
	description := ""
	if task != nil {
		description = task.Description
	}
	text := strings.ToLower(strings.Join([]string{benchmarkTaskString(task, "benchmark_mode"), description}, " "))
	return strings.Contains(text, "latency") || strings.Contains(text, "throughput") || strings.Contains(text, "inference") || strings.Contains(text, "推理") || strings.Contains(text, "延迟") || strings.Contains(text, "吞吐")
}

func benchmarkTaskString(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil || task.Inputs[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(task.Inputs[key]))
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
