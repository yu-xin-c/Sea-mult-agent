package agent

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
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
	"time"

	"scholar-agent-backend/internal/models"
)

const (
	experimentWorkspaceDirectory = ".scholar/experiment"
	retrievalCorpusPath          = experimentWorkspaceDirectory + "/corpus.jsonl"
	retrievalSearchPath          = experimentWorkspaceDirectory + "/search_queries.jsonl"
	retrievalHoldoutPath         = experimentWorkspaceDirectory + "/holdout_queries.jsonl"
	retrievalRunnerPath          = experimentWorkspaceDirectory + "/retrieval_runner.py"
	experimentMaxUploadBytes     = 256 * 1024 * 1024
	experimentMaxDocuments       = 200000
	experimentMaxQueries         = 50000
)

//go:embed raglab/runner.py
var retrievalExperimentRunner []byte

type experimentDomainAdapter interface {
	Name() string
	Domain() string
	Matches(task *models.Task, files []benchmarkUploadedFile) bool
	Prepare(ctx context.Context, task *models.Task, files []benchmarkUploadedFile) (string, models.ExperimentDatasetManifest, error)
	BuildSpec(task *models.Task, workspacePath string, manifest models.ExperimentDatasetManifest) (models.ExperimentResearchSpec, error)
}

var experimentDomainAdapters = []experimentDomainAdapter{
	portableExperimentAdapter{},
	retrievalExperimentAdapter{},
}

type retrievalExperimentAdapter struct{}

type experimentInputTable struct {
	Source   benchmarkUploadedFile
	RoleHint string
	Rows     []map[string]any
}

type retrievalTableSelection struct {
	CorpusTable  experimentInputTable
	QueryTable   experimentInputTable
	DocumentID   string
	DocumentText string
	QueryID      string
	QueryText    string
	RelevantIDs  string
	Split        string
	Links        string
}

type retrievalDocument struct {
	ID    string   `json:"id"`
	Text  string   `json:"text"`
	Links []string `json:"links,omitempty"`
}

type retrievalQuery struct {
	ID             string   `json:"id"`
	Query          string   `json:"query"`
	RelevantDocIDs []string `json:"relevant_doc_ids"`
	Split          string   `json:"-"`
}

func (retrievalExperimentAdapter) Name() string   { return "retrieval.v1" }
func (retrievalExperimentAdapter) Domain() string { return "retrieval" }

func (retrievalExperimentAdapter) Matches(task *models.Task, _ []benchmarkUploadedFile) bool {
	text := strings.ToLower(strings.Join([]string{
		benchmarkTaskString(task, "research_domain"),
		benchmarkTaskString(task, "experiment_adapter"),
		taskText(task),
	}, " "))
	return experimentHasAny(text, "retrieval", "rag", "bm25", "graphrag", "graph rag", "检索", "知识库问答")
}

func taskText(task *models.Task) string {
	if task == nil {
		return ""
	}
	return task.Name + " " + task.Description
}

func selectExperimentDomainAdapter(task *models.Task, files []benchmarkUploadedFile) (experimentDomainAdapter, error) {
	requested := strings.ToLower(strings.TrimSpace(benchmarkTaskString(task, "experiment_adapter")))
	domain := strings.ToLower(strings.TrimSpace(benchmarkTaskString(task, "research_domain")))
	for _, adapter := range experimentDomainAdapters {
		if requested != "" && requested != adapter.Name() {
			continue
		}
		if domain != "" && adapter.Domain() != "*" && domain != adapter.Domain() {
			continue
		}
		if adapter.Matches(task, files) {
			return adapter, nil
		}
	}
	return nil, fmt.Errorf("no experiment adapter matched the uploaded task; upload experiment.json or name a supported built-in domain (currently: retrieval)")
}

func (a retrievalExperimentAdapter) Prepare(_ context.Context, task *models.Task, files []benchmarkUploadedFile) (workspace string, manifest models.ExperimentDatasetManifest, err error) {
	tables, sourceHashes, err := loadExperimentInputTables(files)
	if err != nil {
		return "", manifest, err
	}
	selection, err := selectRetrievalTables(task, tables)
	if err != nil {
		return "", manifest, err
	}
	documents, queries, explicitSplit, err := canonicalizeRetrievalData(selection)
	if err != nil {
		return "", manifest, err
	}
	searchQueries, holdoutQueries, splitMethod, err := splitRetrievalQueries(queries, explicitSplit)
	if err != nil {
		return "", manifest, err
	}

	workspace, err = os.MkdirTemp("", "scholar-experiment-")
	if err != nil {
		return "", manifest, fmt.Errorf("create experiment workspace: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(workspace)
		}
	}()
	root := filepath.Join(workspace, filepath.FromSlash(experimentWorkspaceDirectory))
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", manifest, err
	}
	if err := writeExperimentJSONL(filepath.Join(workspace, filepath.FromSlash(retrievalCorpusPath)), documents); err != nil {
		return "", manifest, err
	}
	if err := writeExperimentJSONL(filepath.Join(workspace, filepath.FromSlash(retrievalSearchPath)), searchQueries); err != nil {
		return "", manifest, err
	}
	if err := writeExperimentJSONL(filepath.Join(workspace, filepath.FromSlash(retrievalHoldoutPath)), holdoutQueries); err != nil {
		return "", manifest, err
	}
	if err := os.WriteFile(filepath.Join(workspace, filepath.FromSlash(retrievalRunnerPath)), retrievalExperimentRunner, 0o500); err != nil {
		return "", manifest, err
	}
	frozen, err := hashExperimentFiles(workspace, []string{retrievalCorpusPath, retrievalSearchPath, retrievalHoldoutPath, retrievalRunnerPath})
	if err != nil {
		return "", manifest, err
	}
	hasLinks := false
	for _, document := range documents {
		if len(document.Links) > 0 {
			hasLinks = true
			break
		}
	}
	manifest = models.ExperimentDatasetManifest{
		Version: models.ExperimentDatasetVersion,
		Name:    strings.TrimSuffix(filepath.Base(selection.CorpusTable.Source.Name), filepath.Ext(selection.CorpusTable.Source.Name)),
		Domain:  a.Domain(), Adapter: a.Name(),
		Mapping: map[string]string{
			"corpus_source": selection.CorpusTable.Source.Name, "query_source": selection.QueryTable.Source.Name,
			"document_id": selection.DocumentID, "document_text": selection.DocumentText,
			"query_id": selection.QueryID, "query_text": selection.QueryText, "relevant_ids": selection.RelevantIDs,
		},
		Counts: map[string]int{
			"documents": len(documents), "search_cases": len(searchQueries), "holdout_cases": len(holdoutQueries),
		},
		Capabilities: map[string]bool{"graph_links": hasLinks},
		Assets: map[string]string{
			"corpus": retrievalCorpusPath, "search_cases": retrievalSearchPath,
			"holdout_cases": retrievalHoldoutPath, "runner": retrievalRunnerPath,
		},
		SplitMethod: splitMethod, SourceFiles: sourceHashes, FrozenFiles: frozen, CreatedAt: time.Now().UTC(),
	}
	if selection.Split != "" {
		manifest.Mapping["split"] = selection.Split
	}
	if selection.Links != "" {
		manifest.Mapping["links"] = selection.Links
	}
	succeeded = true
	return workspace, manifest, nil
}

func (a retrievalExperimentAdapter) BuildSpec(task *models.Task, workspacePath string, manifest models.ExperimentDatasetManifest) (models.ExperimentResearchSpec, error) {
	if manifest.Version != models.ExperimentDatasetVersion || manifest.Adapter != a.Name() {
		return models.ExperimentResearchSpec{}, fmt.Errorf("dataset manifest is not compatible with %s", a.Name())
	}
	if err := verifyExperimentFiles(workspacePath, manifest.FrozenFiles); err != nil {
		return models.ExperimentResearchSpec{}, err
	}
	metric := strings.ToLower(strings.TrimSpace(benchmarkTaskString(task, "experiment_metric")))
	if metric == "" {
		metric = "ndcg_at_k"
	}
	if metric != "ndcg_at_k" && metric != "mrr" && metric != "recall_at_k" {
		return models.ExperimentResearchSpec{}, fmt.Errorf("retrieval metric %q is unsupported; choose ndcg_at_k, mrr, or recall_at_k", metric)
	}
	cutoff := boundedTaskInt(task, "experiment_cutoff", 5, 1, 100)
	maxTrials := boundedTaskInt(task, "experiment_max_trials", 12, 1, 40)
	maxParallelTrials := boundedTaskInt(task, "experiment_max_parallel_trials", 1, 1, 4)
	maxWall := boundedTaskInt(task, "experiment_max_wall_seconds", 900, 30, 3600)
	validationRuns := boundedTaskInt(task, "experiment_validation_runs", 3, 1, 5)
	target, err := optionalExperimentTaskFloat(task, "experiment_target_score", 0, 1)
	if err != nil {
		return models.ExperimentResearchSpec{}, err
	}
	holdoutTarget, err := optionalExperimentTaskFloat(task, "experiment_holdout_target_score", 0, 1)
	if err != nil {
		return models.ExperimentResearchSpec{}, err
	}
	if holdoutTarget == nil && target != nil {
		value := *target
		holdoutTarget = &value
	}
	strategies := retrievalExperimentStrategies(manifest.Capabilities["graph_links"])
	containerPath := func(relative string) string { return "/workspace/" + filepath.ToSlash(relative) }
	command := func(cases string) []string {
		return []string{
			"python3", containerPath(manifest.Assets["runner"]),
			"--corpus", containerPath(manifest.Assets["corpus"]),
			"--queries", containerPath(cases),
			"--config", "{config_path}", "--cutoff", strconv.Itoa(cutoff),
		}
	}
	evaluationIsolation := models.ExperimentExecutionSerial
	if maxParallelTrials > 1 {
		evaluationIsolation = models.ExperimentExecutionReadOnly
	}
	return models.ExperimentResearchSpec{
		Version: models.ExperimentSpecVersion,
		Name:    chooseNonEmpty(manifest.Name, "retrieval") + " retrieval strategy search",
		Domain:  a.Domain(), Adapter: a.Name(), CandidateKind: "strategy_config",
		Objective:     fmt.Sprintf("maximize %s at a fixed cutoff k=%d on frozen business data", metric, cutoff),
		SearchCommand: command(manifest.Assets["search_cases"]), HoldoutCommand: command(manifest.Assets["holdout_cases"]),
		Strategies: strategies, MetricKey: metric, Direction: "maximize", MinDelta: 0.0001,
		TargetScore: target, HoldoutTargetScore: holdoutTarget, MaxTrials: maxTrials,
		MaxParallelTrials: maxParallelTrials, EvaluationIsolation: evaluationIsolation,
		MaxWallSeconds: maxWall, ValidationRuns: validationRuns,
		Dependencies: []string{"rank-bm25>=0.2.2,<0.3", "scikit-learn>=1.4,<2", "networkx>=3.2,<4"},
		FrozenFiles:  append([]models.ResearchFileHash(nil), manifest.FrozenFiles...), CreatedAt: time.Now().UTC(),
	}, nil
}

func retrievalExperimentStrategies(includeGraph bool) []models.ExperimentStrategy {
	parameter := func(name, description string, values []any, defaultValue any) models.ExperimentParameter {
		return models.ExperimentParameter{Name: name, Description: description, Values: values, Default: defaultValue}
	}
	bm25 := []models.ExperimentParameter{
		parameter("k1", "term-frequency saturation", []any{0.8, 1.2, 1.5, 1.8, 2.2}, 1.5),
		parameter("b", "document-length normalization", []any{0.2, 0.5, 0.75, 1.0}, 0.75),
	}
	tfidf := []models.ExperimentParameter{
		parameter("ngram_max", "maximum token n-gram", []any{1, 2}, 1),
		parameter("sublinear_tf", "log-scale term frequency", []any{false, true}, true),
	}
	hybrid := append(append([]models.ExperimentParameter{}, bm25...), tfidf...)
	hybrid = append(hybrid,
		parameter("alpha", "BM25 share in reciprocal-rank fusion", []any{0.2, 0.5, 0.8}, 0.5),
		parameter("rrf_k", "reciprocal-rank smoothing", []any{10, 30, 60}, 60),
	)
	strategies := []models.ExperimentStrategy{
		{Name: "bm25", Description: "sparse lexical BM25 baseline", Parameters: bm25},
		{Name: "tfidf", Description: "TF-IDF cosine retrieval", Parameters: tfidf},
		{Name: "hybrid_rrf", Description: "BM25 and TF-IDF reciprocal-rank fusion", Parameters: hybrid},
	}
	if includeGraph {
		graph := append([]models.ExperimentParameter{}, hybrid...)
		graph = append(graph,
			parameter("graph_weight", "linked-document propagation weight", []any{0.25, 0.75, 1.25}, 1.25),
			parameter("graph_depth", "maximum graph expansion hops", []any{1, 2}, 1),
		)
		strategies = append(strategies, models.ExperimentStrategy{
			Name: "graph_hybrid", Description: "hybrid retrieval with explicit corpus-link expansion (not Microsoft GraphRAG)", Parameters: graph,
		})
	}
	return strategies
}

func loadExperimentInputTables(files []benchmarkUploadedFile) ([]experimentInputTable, []models.ResearchFileHash, error) {
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("uploaded research data is required")
	}
	tables := make([]experimentInputTable, 0, len(files))
	sourceHashes := make([]models.ResearchFileHash, 0, len(files))
	for _, file := range files {
		extension := strings.ToLower(filepath.Ext(file.Name))
		if extension != ".csv" && extension != ".tsv" && extension != ".json" && extension != ".jsonl" {
			continue
		}
		path := filepath.Clean(strings.TrimSpace(file.StoragePath))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("uploaded file %q is unavailable or not a regular file", file.Name)
		}
		if info.Size() <= 0 || info.Size() > experimentMaxUploadBytes {
			return nil, nil, fmt.Errorf("uploaded file %q must be between 1 byte and 256 MiB", file.Name)
		}
		hash, err := sha256File(path)
		if err != nil {
			return nil, nil, err
		}
		if file.SHA256 != "" && !strings.EqualFold(file.SHA256, hash) {
			return nil, nil, fmt.Errorf("uploaded file %q checksum mismatch", file.Name)
		}
		file.StoragePath = path
		loaded, err := readExperimentTables(file)
		if err != nil {
			return nil, nil, err
		}
		tables = append(tables, loaded...)
		sourceHashes = append(sourceHashes, models.ResearchFileHash{Path: filepath.Base(file.Name), SHA256: hash})
	}
	if len(tables) == 0 {
		return nil, nil, fmt.Errorf("no supported research dataset found; expected csv, tsv, json, or jsonl")
	}
	sort.Slice(sourceHashes, func(i, j int) bool { return sourceHashes[i].Path < sourceHashes[j].Path })
	return tables, sourceHashes, nil
}

func readExperimentTables(file benchmarkUploadedFile) ([]experimentInputTable, error) {
	switch strings.ToLower(filepath.Ext(file.Name)) {
	case ".csv", ".tsv":
		delimiter := ','
		if strings.HasSuffix(strings.ToLower(file.Name), ".tsv") {
			delimiter = '\t'
		}
		rows, err := readExperimentDelimited(file.StoragePath, delimiter)
		return []experimentInputTable{{Source: file, RoleHint: retrievalRoleHint(file.Name), Rows: rows}}, err
	case ".jsonl":
		rows, err := readExperimentJSONL(file.StoragePath)
		return []experimentInputTable{{Source: file, RoleHint: retrievalRoleHint(file.Name), Rows: rows}}, err
	case ".json":
		return readExperimentJSON(file)
	default:
		return nil, fmt.Errorf("unsupported experiment file %q", file.Name)
	}
}

func readExperimentDelimited(path string, delimiter rune) ([]map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read experiment header: %w", err)
	}
	header = normalizeBenchmarkHeader(header)
	rows := make([]map[string]any, 0, 256)
	for {
		record, readErr := reader.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		row := make(map[string]any, len(header))
		for index, name := range header {
			if index < len(record) {
				row[name] = record[index]
			} else {
				row[name] = ""
			}
		}
		rows = append(rows, row)
		if len(rows) > experimentMaxDocuments+experimentMaxQueries {
			return nil, fmt.Errorf("experiment input exceeds the bounded record limit")
		}
	}
	return rows, nil
}

func readExperimentJSONL(path string) ([]map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows := make([]map[string]any, 0, 256)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("decode experiment JSONL row %d: %w", len(rows)+1, err)
		}
		rows = append(rows, row)
		if len(rows) > experimentMaxDocuments+experimentMaxQueries {
			return nil, fmt.Errorf("experiment input exceeds the bounded record limit")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func readExperimentJSON(file benchmarkUploadedFile) ([]experimentInputTable, error) {
	handle, err := os.Open(file.StoragePath)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	decoder := json.NewDecoder(io.LimitReader(handle, experimentMaxUploadBytes+1))
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode experiment JSON %q: %w", file.Name, err)
	}
	tables := []experimentInputTable{}
	appendValues := func(role string, values []any) error {
		rows := make([]map[string]any, 0, len(values))
		for index, value := range values {
			row, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("%s record %d is not an object", file.Name, index+1)
			}
			rows = append(rows, row)
		}
		tables = append(tables, experimentInputTable{Source: file, RoleHint: role, Rows: rows})
		return nil
	}
	switch value := payload.(type) {
	case []any:
		if err := appendValues(retrievalRoleHint(file.Name), value); err != nil {
			return nil, err
		}
	case map[string]any:
		for key, raw := range value {
			values, ok := raw.([]any)
			if !ok {
				continue
			}
			role := retrievalRoleHint(key)
			if role == "" && experimentHasAny(strings.ToLower(key), "data", "records", "items", "examples") {
				role = retrievalRoleHint(file.Name)
			}
			if err := appendValues(role, values); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("experiment JSON %q must contain arrays of objects", file.Name)
	}
	if len(tables) == 0 {
		return nil, fmt.Errorf("experiment JSON %q contains no record arrays", file.Name)
	}
	return tables, nil
}

func retrievalRoleHint(value string) string {
	normalized := strings.ToLower(value)
	if experimentHasAny(normalized, "corpus", "document", "docs", "passage", "knowledge", "语料", "文档") {
		return "corpus"
	}
	if experimentHasAny(normalized, "query", "queries", "question", "eval", "benchmark", "问题", "评测") {
		return "queries"
	}
	return ""
}

func selectRetrievalTables(task *models.Task, tables []experimentInputTable) (retrievalTableSelection, error) {
	var best retrievalTableSelection
	bestCorpusScore, bestQueryScore := -1, -1
	for _, table := range tables {
		if len(table.Rows) == 0 {
			continue
		}
		documentID := experimentColumn(table.Rows, benchmarkTaskString(task, "document_id_column"), []string{"doc_id", "document_id", "id", "chunk_id", "passage_id"})
		documentText := experimentColumn(table.Rows, benchmarkTaskString(task, "document_text_column"), []string{"text", "content", "document", "passage", "body", "chunk"})
		corpusScore := 0
		if documentID != "" {
			corpusScore += 2
		}
		if documentText != "" {
			corpusScore += 3
		}
		if table.RoleHint == "corpus" {
			corpusScore += 3
		}
		if corpusScore > bestCorpusScore && documentID != "" && documentText != "" {
			bestCorpusScore = corpusScore
			best.CorpusTable, best.DocumentID, best.DocumentText = table, documentID, documentText
			best.Links = experimentColumn(table.Rows, benchmarkTaskString(task, "document_links_column"), []string{"links", "neighbors", "related_doc_ids", "citations", "edges"})
		}

		queryID := experimentColumn(table.Rows, benchmarkTaskString(task, "query_id_column"), []string{"query_id", "qid", "question_id", "id"})
		queryText := experimentColumn(table.Rows, benchmarkTaskString(task, "query_text_column"), []string{"query", "question", "prompt", "search_text"})
		relevant := experimentColumn(table.Rows, benchmarkTaskString(task, "relevant_ids_column"), []string{"relevant_doc_ids", "relevant_ids", "positive_doc_ids", "gold_doc_ids", "doc_id", "document_id"})
		queryScore := 0
		if queryID != "" {
			queryScore++
		}
		if queryText != "" {
			queryScore += 3
		}
		if relevant != "" {
			queryScore += 3
		}
		if table.RoleHint == "queries" {
			queryScore += 3
		}
		if queryScore > bestQueryScore && queryID != "" && queryText != "" && relevant != "" {
			bestQueryScore = queryScore
			best.QueryTable, best.QueryID, best.QueryText, best.RelevantIDs = table, queryID, queryText, relevant
			best.Split = experimentColumn(table.Rows, benchmarkTaskString(task, "query_split_column"), []string{"split", "subset", "partition"})
		}
	}
	if bestCorpusScore < 0 {
		return best, fmt.Errorf("could not identify corpus columns; expected a document ID and text/content column")
	}
	if bestQueryScore < 0 {
		return best, fmt.Errorf("could not identify evaluation queries; expected query ID, query text, and relevant document IDs")
	}
	return best, nil
}

func experimentColumn(rows []map[string]any, hint string, aliases []string) string {
	columns := map[string]string{}
	for _, row := range rows {
		for key := range row {
			columns[strings.ToLower(strings.TrimSpace(key))] = key
		}
	}
	if hint != "" {
		return columns[strings.ToLower(strings.TrimSpace(hint))]
	}
	for _, alias := range aliases {
		if actual := columns[strings.ToLower(alias)]; actual != "" {
			return actual
		}
	}
	return ""
}

func canonicalizeRetrievalData(selection retrievalTableSelection) ([]retrievalDocument, []retrievalQuery, bool, error) {
	documents := make([]retrievalDocument, 0, len(selection.CorpusTable.Rows))
	documentIDs := map[string]struct{}{}
	for index, row := range selection.CorpusTable.Rows {
		id := experimentString(row[selection.DocumentID])
		text := strings.TrimSpace(experimentString(row[selection.DocumentText]))
		if id == "" || text == "" {
			return nil, nil, false, fmt.Errorf("corpus record %d has an empty document ID or text", index+1)
		}
		if _, exists := documentIDs[id]; exists {
			return nil, nil, false, fmt.Errorf("duplicate document ID %q", id)
		}
		documentIDs[id] = struct{}{}
		documents = append(documents, retrievalDocument{ID: id, Text: text, Links: experimentStringList(row[selection.Links])})
		if len(documents) > experimentMaxDocuments {
			return nil, nil, false, fmt.Errorf("corpus exceeds %d documents", experimentMaxDocuments)
		}
	}
	for index := range documents {
		links := documents[index].Links[:0]
		for _, link := range documents[index].Links {
			if _, ok := documentIDs[link]; ok && link != documents[index].ID {
				links = append(links, link)
			}
		}
		documents[index].Links = uniqueExperimentStrings(links)
	}

	queries := make([]retrievalQuery, 0, len(selection.QueryTable.Rows))
	queryIDs := map[string]struct{}{}
	explicitSplit := selection.Split != ""
	for index, row := range selection.QueryTable.Rows {
		id := experimentString(row[selection.QueryID])
		queryText := strings.TrimSpace(experimentString(row[selection.QueryText]))
		relevant := uniqueExperimentStrings(experimentStringList(row[selection.RelevantIDs]))
		if id == "" || queryText == "" || len(relevant) == 0 {
			return nil, nil, false, fmt.Errorf("query record %d requires query ID, query text, and at least one relevant document ID", index+1)
		}
		if _, exists := queryIDs[id]; exists {
			return nil, nil, false, fmt.Errorf("duplicate query ID %q", id)
		}
		queryIDs[id] = struct{}{}
		for _, relevantID := range relevant {
			if _, ok := documentIDs[relevantID]; !ok {
				return nil, nil, false, fmt.Errorf("query %q references unknown document ID %q", id, relevantID)
			}
		}
		split := normalizeExperimentSplit(experimentString(row[selection.Split]))
		if explicitSplit && split == "" {
			return nil, nil, false, fmt.Errorf("query %q has an unsupported or empty split; use search/train/dev or holdout/test/validation", id)
		}
		queries = append(queries, retrievalQuery{ID: id, Query: queryText, RelevantDocIDs: relevant, Split: split})
		if len(queries) > experimentMaxQueries {
			return nil, nil, false, fmt.Errorf("evaluation data exceeds %d queries", experimentMaxQueries)
		}
	}
	if len(documents) < 2 || len(queries) < 2 {
		return nil, nil, false, fmt.Errorf("a trustworthy campaign needs at least 2 documents and 2 labeled queries")
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].ID < documents[j].ID })
	sort.Slice(queries, func(i, j int) bool { return queries[i].ID < queries[j].ID })
	return documents, queries, explicitSplit, nil
}

func splitRetrievalQueries(queries []retrievalQuery, explicit bool) ([]retrievalQuery, []retrievalQuery, string, error) {
	if !explicit {
		for index := range queries {
			hash := sha256.Sum256([]byte(queries[index].ID))
			if int(hash[0])%5 == 0 {
				queries[index].Split = "holdout"
			} else {
				queries[index].Split = "search"
			}
		}
	}
	search, holdout := []retrievalQuery{}, []retrievalQuery{}
	for _, query := range queries {
		if query.Split == "holdout" {
			holdout = append(holdout, query)
		} else {
			search = append(search, query)
		}
	}
	if !explicit {
		if len(holdout) == 0 {
			holdout = append(holdout, search[len(search)-1])
			search = search[:len(search)-1]
		}
		if len(search) == 0 {
			search = append(search, holdout[0])
			holdout = holdout[1:]
		}
	}
	if len(search) == 0 || len(holdout) == 0 {
		return nil, nil, "", fmt.Errorf("evaluation queries must contain both search and holdout cases")
	}
	method := "deterministic_sha256_80_20"
	if explicit {
		method = "explicit_column"
	}
	return search, holdout, method, nil
}

func normalizeExperimentSplit(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "search", "train", "training", "dev", "development", "public":
		return "search"
	case "holdout", "test", "testing", "validation", "valid", "hidden", "private":
		return "holdout"
	default:
		return ""
	}
}

func experimentString(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func experimentStringList(value any) []string {
	if value == nil {
		return nil
	}
	values := []string{}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if text := experimentString(item); text != "" {
				values = append(values, text)
			}
		}
	case []string:
		values = append(values, typed...)
	case string:
		trimmed := strings.TrimSpace(typed)
		var decoded []any
		if strings.HasPrefix(trimmed, "[") && json.Unmarshal([]byte(trimmed), &decoded) == nil {
			return experimentStringList(decoded)
		}
		values = strings.FieldsFunc(trimmed, func(r rune) bool { return r == ',' || r == ';' || r == '|' })
	default:
		if text := experimentString(typed); text != "" {
			values = append(values, text)
		}
	}
	for index := range values {
		values[index] = strings.TrimSpace(values[index])
	}
	return uniqueExperimentStrings(values)
}

func uniqueExperimentStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func writeExperimentJSONL(path string, values any) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	reflection, err := json.Marshal(values)
	if err != nil {
		_ = file.Close()
		return err
	}
	var records []json.RawMessage
	if err := json.Unmarshal(reflection, &records); err != nil {
		_ = file.Close()
		return err
	}
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			_ = file.Close()
			return err
		}
	}
	return file.Close()
}

func hashExperimentFiles(workspacePath string, relativePaths []string) ([]models.ResearchFileHash, error) {
	hashes := make([]models.ResearchFileHash, 0, len(relativePaths))
	for _, relative := range relativePaths {
		path, err := benchmarkPathInWorkspace(workspacePath, relative)
		if err != nil {
			return nil, err
		}
		hash, err := sha256File(path)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, models.ResearchFileHash{Path: filepath.ToSlash(relative), SHA256: hash})
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i].Path < hashes[j].Path })
	return hashes, nil
}

func verifyExperimentFiles(workspacePath string, expected []models.ResearchFileHash) error {
	paths := make([]string, 0, len(expected))
	for _, item := range expected {
		paths = append(paths, item.Path)
	}
	observed, err := hashExperimentFiles(workspacePath, paths)
	if err != nil {
		return err
	}
	if len(observed) != len(expected) {
		return fmt.Errorf("experiment protected file set changed")
	}
	expectedMap := map[string]string{}
	for _, item := range expected {
		expectedMap[item.Path] = strings.ToLower(item.SHA256)
	}
	for _, item := range observed {
		if expectedMap[item.Path] != strings.ToLower(item.SHA256) {
			return fmt.Errorf("experiment protected file changed: %s", item.Path)
		}
	}
	return nil
}

func optionalExperimentTaskFloat(task *models.Task, key string, minimum, maximum float64) (*float64, error) {
	raw := strings.TrimSpace(benchmarkTaskString(task, key))
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, fmt.Errorf("%s must be a number", key)
	}
	if value < minimum || value > maximum {
		return nil, fmt.Errorf("%s must be between %g and %g", key, minimum, maximum)
	}
	return &value, nil
}

func experimentJSONSHA256(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}

func experimentHasAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}
