package models

// DatasetColumnProfile records a bounded, non-sensitive summary of one dataset column.
type DatasetColumnProfile struct {
	Name         string `json:"name"`
	InferredType string `json:"inferred_type"`
	NonNullCount int    `json:"non_null_count"`
	UniqueCount  int    `json:"unique_count"`
}

// DatasetManifest is the deterministic contract produced before adapter generation.
type DatasetManifest struct {
	Version              string                 `json:"version"`
	Name                 string                 `json:"name"`
	Format               string                 `json:"format"`
	SHA256               string                 `json:"sha256"`
	Size                 int64                  `json:"size"`
	RowCount             int                    `json:"row_count"`
	Columns              []DatasetColumnProfile `json:"columns"`
	InputColumn          string                 `json:"input_column,omitempty"`
	TargetColumn         string                 `json:"target_column,omitempty"`
	SuggestedTask        string                 `json:"suggested_task"`
	MappingConfidence    float64                `json:"mapping_confidence"`
	RequiresConfirmation bool                   `json:"requires_confirmation"`
	SamplePreview        []map[string]string    `json:"sample_preview,omitempty"`
}

// BenchmarkAdapterCandidate is one repository entry-point strategy considered by the coding sub-agent.
type BenchmarkAdapterCandidate struct {
	Kind       string  `json:"kind"`
	EntryPoint string  `json:"entrypoint"`
	Confidence float64 `json:"confidence"`
	Evidence   string  `json:"evidence"`
	Risk       string  `json:"risk,omitempty"`
}

// BenchmarkAdapterPlan records the bounded entry-point selection before code generation.
type BenchmarkAdapterPlan struct {
	Status        string                      `json:"status"`
	Candidates    []BenchmarkAdapterCandidate `json:"candidates"`
	SelectedIndex int                         `json:"selected_index"`
	Reason        string                      `json:"reason"`
}

// BenchmarkAdapterSpec is the replayable contract for generated benchmark code.
type BenchmarkAdapterSpec struct {
	Version           string   `json:"version"`
	Status            string   `json:"status"`
	Strategy          string   `json:"strategy"`
	EntryPoint        string   `json:"entrypoint"`
	Confidence        float64  `json:"confidence"`
	DatasetSHA256     string   `json:"dataset_sha256"`
	InputColumn       string   `json:"input_column,omitempty"`
	TargetColumn      string   `json:"target_column,omitempty"`
	Metrics           []string `json:"metrics"`
	Dependencies      []string `json:"dependencies,omitempty"`
	AdapterCodeSHA256 string   `json:"adapter_code_sha256"`
	RepairAttempts    int      `json:"repair_attempts"`
	Reason            string   `json:"reason"`
}

// BenchmarkAttempt records one bounded preflight or execution attempt.
type BenchmarkAttempt struct {
	Attempt  int    `json:"attempt"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
	Repaired bool   `json:"repaired"`
}

// BenchmarkRunManifest is emitted by the generated adapter and checked deterministically.
type BenchmarkRunManifest struct {
	Status        string `json:"status"`
	DatasetSHA256 string `json:"dataset_sha256"`
	SampleCount   int    `json:"sample_count"`
	Seed          int    `json:"seed,omitempty"`
	Adapter       string `json:"adapter,omitempty"`
}

// BenchmarkHarnessReport summarizes bounded execution and deterministic validation.
type BenchmarkHarnessReport struct {
	Status          string             `json:"status"`
	Mode            string             `json:"mode"`
	Attempts        []BenchmarkAttempt `json:"attempts"`
	SampleCount     int                `json:"sample_count,omitempty"`
	Metrics         map[string]float64 `json:"metrics,omitempty"`
	PredictionsPath string             `json:"predictions_path,omitempty"`
	Reason          string             `json:"reason,omitempty"`
}
