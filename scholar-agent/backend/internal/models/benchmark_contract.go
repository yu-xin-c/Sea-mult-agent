package models

import "time"

const (
	BenchmarkAuditVersion          = "benchmark.audit/v1"
	BenchmarkSplitVersion          = "benchmark.split/v1"
	BenchmarkMetricContractVersion = "benchmark.metric-contract/v1"
	BenchmarkRewardContractVersion = "benchmark.reward-contract/v1"
	BenchmarkContractVersion       = "benchmark.contract/v1"
	BenchmarkValidationVersion     = "benchmark.validation/v1"
)

// BenchmarkDatasetAudit records deterministic task and schema inference before
// any repository code is allowed to see the dataset.
type BenchmarkDatasetAudit struct {
	Version        string            `json:"version"`
	DatasetSHA256  string            `json:"dataset_sha256"`
	TaskType       string            `json:"task_type"`
	InputColumns   []string          `json:"input_columns"`
	TargetColumn   string            `json:"target_column,omitempty"`
	GroupColumn    string            `json:"group_column,omitempty"`
	TimeColumn     string            `json:"time_column,omitempty"`
	Confidence     float64           `json:"confidence"`
	Reasons        []string          `json:"reasons"`
	ClassCounts    map[string]int    `json:"class_counts,omitempty"`
	MissingCounts  map[string]int    `json:"missing_counts"`
	BlockingIssues []string          `json:"blocking_issues,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

// BenchmarkSplitArtifact identifies one immutable public or private split.
// Private paths are deliberately omitted from serialized contracts.
type BenchmarkSplitArtifact struct {
	Name         string         `json:"name"`
	RelativePath string         `json:"relative_path,omitempty"`
	SHA256       string         `json:"sha256"`
	RowCount     int            `json:"row_count"`
	TargetPublic bool           `json:"target_public"`
	Distribution map[string]int `json:"distribution,omitempty"`
}

// BenchmarkSplitManifest freezes how one uploaded dataset became train,
// validation, and hidden test material.
type BenchmarkSplitManifest struct {
	Version           string                            `json:"version"`
	ID                string                            `json:"id"`
	DatasetSHA256     string                            `json:"dataset_sha256"`
	TaskType          string                            `json:"task_type"`
	Method            string                            `json:"method"`
	Seed              int                               `json:"seed"`
	Ratios            map[string]float64                `json:"ratios"`
	InputColumns      []string                          `json:"input_columns"`
	TargetColumn      string                            `json:"target_column,omitempty"`
	GroupColumn       string                            `json:"group_column,omitempty"`
	TimeColumn        string                            `json:"time_column,omitempty"`
	Splits            map[string]BenchmarkSplitArtifact `json:"splits"`
	PreflightFeatures *BenchmarkSplitArtifact           `json:"preflight_features,omitempty"`
	PrivateStateID    string                            `json:"private_state_id,omitempty"`
	HiddenLabelsSHA   string                            `json:"hidden_labels_sha256,omitempty"`
	SourceRowCount    int                               `json:"source_row_count"`
	EligibleRowCount  int                               `json:"eligible_row_count"`
	ExcludedRowCount  int                               `json:"excluded_row_count"`
	MaterializedAt    time.Time                         `json:"materialized_at"`
}

// BenchmarkLeakageReport makes split quality independently inspectable.
type BenchmarkLeakageReport struct {
	Version                   string   `json:"version"`
	Status                    string   `json:"status"`
	ExactDuplicateGroups      int      `json:"exact_duplicate_groups"`
	ConflictingTargetGroups   int      `json:"conflicting_target_groups"`
	CrossSplitInputOverlaps   int      `json:"cross_split_input_overlaps"`
	CrossSplitGroupOverlaps   int      `json:"cross_split_group_overlaps"`
	ChronologyViolations      int      `json:"chronology_violations"`
	NearDuplicatePairsChecked int      `json:"near_duplicate_pairs_checked"`
	NearDuplicateCrossSplit   int      `json:"near_duplicate_cross_split"`
	Checks                    []string `json:"checks"`
	Warnings                  []string `json:"warnings,omitempty"`
}

// BenchmarkMetricDefinition describes one independently recomputable metric.
type BenchmarkMetricDefinition struct {
	Name        string   `json:"name"`
	Direction   string   `json:"direction"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	Description string   `json:"description"`
}

type BenchmarkMetricContract struct {
	Version          string                      `json:"version"`
	TaskType         string                      `json:"task_type"`
	PrimaryMetric    string                      `json:"primary_metric"`
	Direction        string                      `json:"direction"`
	MinDelta         float64                     `json:"min_delta"`
	TargetScore      *float64                    `json:"target_score,omitempty"`
	Metrics          []BenchmarkMetricDefinition `json:"metrics"`
	Aggregation      string                      `json:"aggregation"`
	ValidationRuns   int                         `json:"validation_runs"`
	EvaluatorVersion string                      `json:"evaluator_version"`
}

// BenchmarkRewardContract controls candidate ordering only. It never replaces
// the primary metric, Keep/Reject rule, or hidden-test acceptance.
type BenchmarkRewardContract struct {
	Version                  string  `json:"version"`
	QualityTransform         string  `json:"quality_transform"`
	BaselineNormalization    string  `json:"baseline_normalization"`
	DurationPenaltyPerSecond float64 `json:"duration_penalty_per_second"`
	FailurePenalty           float64 `json:"failure_penalty"`
	Usage                    string  `json:"usage"`
}

type BenchmarkContract struct {
	Version               string                  `json:"version"`
	ID                    string                  `json:"id"`
	DatasetSHA256         string                  `json:"dataset_sha256"`
	SplitManifestSHA256   string                  `json:"split_manifest_sha256"`
	TaskType              string                  `json:"task_type"`
	InputColumns          []string                `json:"input_columns"`
	TargetColumn          string                  `json:"target_column,omitempty"`
	Metric                BenchmarkMetricContract `json:"metric"`
	Reward                BenchmarkRewardContract `json:"reward"`
	PublicEvaluatorSHA256 string                  `json:"public_evaluator_sha256"`
	HiddenEvaluatorSHA256 string                  `json:"hidden_evaluator_sha256"`
	FrozenAt              time.Time               `json:"frozen_at"`
}

type BenchmarkValidationReport struct {
	Version             string             `json:"version"`
	Status              string             `json:"status"`
	ContractID          string             `json:"contract_id"`
	DatasetSHA256       string             `json:"dataset_sha256"`
	PrimaryMetric       string             `json:"primary_metric"`
	Direction           string             `json:"direction"`
	PublicMetrics       map[string]float64 `json:"public_metrics,omitempty"`
	HiddenMetrics       map[string]float64 `json:"hidden_metrics"`
	HiddenSampleCount   int                `json:"hidden_sample_count"`
	TargetReached       bool               `json:"target_reached"`
	ProtectedFilesValid bool               `json:"protected_files_valid"`
	Checks              []string           `json:"checks"`
	ValidatedAt         time.Time          `json:"validated_at"`
}
