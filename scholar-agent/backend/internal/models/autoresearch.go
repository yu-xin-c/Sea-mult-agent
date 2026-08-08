package models

import "time"

const (
	AutoResearchSpecVersion       = "autoresearch.spec/v1"
	AutoResearchLedgerVersion     = "autoresearch.ledger/v1"
	AutoResearchValidationVersion = "autoresearch.validation/v1"
)

// ResearchSpec freezes the editable scope, evaluator and experiment budget for
// one bounded AutoResearch campaign.
type ResearchSpec struct {
	Version         string             `json:"version"`
	Name            string             `json:"name"`
	Objective       string             `json:"objective"`
	EditableFiles   []string           `json:"editable_files"`
	ProtectedFiles  []string           `json:"protected_files"`
	EvalCommand     []string           `json:"eval_command"`
	GuardCommands   [][]string         `json:"guard_commands,omitempty"`
	MetricKey       string             `json:"metric_key"`
	Direction       string             `json:"direction"`
	MinDelta        float64            `json:"min_delta"`
	MaxTrials       int                `json:"max_trials"`
	MaxWallSeconds  int                `json:"max_wall_seconds"`
	ValidationRuns  int                `json:"validation_runs,omitempty"`
	Dependencies    []string           `json:"dependencies,omitempty"`
	FrozenProtected []ResearchFileHash `json:"frozen_protected"`
	FrozenWorkspace string             `json:"frozen_workspace_sha256"`
	Source          string             `json:"source,omitempty"`
	SourceSHA256    string             `json:"source_sha256,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
}

// ResearchFileHash identifies the exact bytes used by a campaign.
type ResearchFileHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// ResearchCommandResult is the bounded, auditable result of one frozen command.
type ResearchCommandResult struct {
	Command       []string `json:"command"`
	ExitCode      int      `json:"exit_code"`
	DurationMS    int64    `json:"duration_ms"`
	StdoutPreview string   `json:"stdout_preview,omitempty"`
	StderrPreview string   `json:"stderr_preview,omitempty"`
	Error         string   `json:"error,omitempty"`
}

// ResearchPatch records a full-file candidate replacement proposed by the
// coding model and applied by the deterministic harness.
type ResearchPatch struct {
	Path         string `json:"path"`
	Reason       string `json:"reason"`
	BeforeSHA256 string `json:"before_sha256"`
	AfterSHA256  string `json:"after_sha256"`
}

// ResearchTrial records one baseline or candidate evaluation.
type ResearchTrial struct {
	Number        int                     `json:"number"`
	Status        string                  `json:"status"`
	Hypothesis    string                  `json:"hypothesis,omitempty"`
	Decision      string                  `json:"decision"`
	Reason        string                  `json:"reason"`
	Patches       []ResearchPatch         `json:"patches,omitempty"`
	GuardResults  []ResearchCommandResult `json:"guard_results,omitempty"`
	EvalResult    ResearchCommandResult   `json:"eval_result"`
	Metric        *float64                `json:"metric,omitempty"`
	DeltaFromBest *float64                `json:"delta_from_best,omitempty"`
	StartedAt     time.Time               `json:"started_at"`
	FinishedAt    time.Time               `json:"finished_at"`
}

// ResearchResourceUsage summarizes deterministic command execution without
// pretending that wall time is equivalent to GPU or monetary cost.
type ResearchResourceUsage struct {
	CommandRuns        int   `json:"command_runs"`
	GuardRuns          int   `json:"guard_runs"`
	EvaluatorRuns      int   `json:"evaluator_runs"`
	SuccessfulCommands int   `json:"successful_commands"`
	FailedCommands     int   `json:"failed_commands"`
	CommandDurationMS  int64 `json:"command_duration_ms"`
	WallDurationMS     int64 `json:"wall_duration_ms"`
}

// ResearchTrialLedger is the append-only evidence record for a campaign.
type ResearchTrialLedger struct {
	Version            string                 `json:"version"`
	SpecSHA256         string                 `json:"spec_sha256"`
	Status             string                 `json:"status"`
	MetricKey          string                 `json:"metric_key"`
	Direction          string                 `json:"direction"`
	BaselineScore      float64                `json:"baseline_score"`
	BestScore          float64                `json:"best_score"`
	MaxTrials          int                    `json:"max_trials"`
	CompletedTrials    int                    `json:"completed_trials"`
	AcceptedTrials     int                    `json:"accepted_trials"`
	ProtectedFiles     []ResearchFileHash     `json:"protected_files"`
	BestCandidateFiles []ResearchFileHash     `json:"best_candidate_files"`
	Trials             []ResearchTrial        `json:"trials"`
	ResourceUsage      *ResearchResourceUsage `json:"resource_usage,omitempty"`
	StopReason         string                 `json:"stop_reason"`
	StartedAt          time.Time              `json:"started_at"`
	FinishedAt         time.Time              `json:"finished_at"`
}

// ResearchValidationRun records one fresh guard/evaluator process sequence.
type ResearchValidationRun struct {
	Number        int                     `json:"number"`
	Status        string                  `json:"status"`
	ObservedScore *float64                `json:"observed_score,omitempty"`
	ScoreMatches  bool                    `json:"score_matches"`
	GuardResults  []ResearchCommandResult `json:"guard_results,omitempty"`
	EvalResult    ResearchCommandResult   `json:"eval_result"`
	Error         string                  `json:"error,omitempty"`
	StartedAt     time.Time               `json:"started_at"`
	FinishedAt    time.Time               `json:"finished_at"`
}

// ResearchBestCandidate is a compact pointer to the retained workspace state.
type ResearchBestCandidate struct {
	SpecSHA256     string             `json:"spec_sha256"`
	Score          float64            `json:"score"`
	MetricKey      string             `json:"metric_key"`
	Direction      string             `json:"direction"`
	AcceptedTrials int                `json:"accepted_trials"`
	Files          []ResearchFileHash `json:"files"`
}

// ResearchValidationReport is produced by an independent rerun of the frozen
// guards and evaluator after the search loop has finished.
type ResearchValidationReport struct {
	Version         string                  `json:"version"`
	Status          string                  `json:"status"`
	SpecSHA256      string                  `json:"spec_sha256"`
	LedgerSHA256    string                  `json:"ledger_sha256"`
	ExpectedScore   float64                 `json:"expected_score"`
	ObservedScore   float64                 `json:"observed_score"`
	ObservedScores  []float64               `json:"observed_scores"`
	MeanScore       float64                 `json:"mean_score"`
	StdDev          float64                 `json:"stddev"`
	MinScore        float64                 `json:"min_score"`
	MaxScore        float64                 `json:"max_score"`
	MetricKey       string                  `json:"metric_key"`
	ScoreMatches    bool                    `json:"score_matches"`
	RequestedRuns   int                     `json:"requested_runs"`
	CompletedRuns   int                     `json:"completed_runs"`
	PassedRuns      int                     `json:"passed_runs"`
	FailedRuns      int                     `json:"failed_runs"`
	UnfinishedRuns  int                     `json:"unfinished_runs"`
	FailureRate     float64                 `json:"failure_rate"`
	ProtectedIntact bool                    `json:"protected_intact"`
	WorkspaceIntact bool                    `json:"workspace_intact"`
	CandidateIntact bool                    `json:"candidate_intact"`
	GuardResults    []ResearchCommandResult `json:"guard_results,omitempty"`
	EvalResult      ResearchCommandResult   `json:"eval_result"`
	Runs            []ResearchValidationRun `json:"runs"`
	ResourceUsage   ResearchResourceUsage   `json:"resource_usage"`
	ValidatedAt     time.Time               `json:"validated_at"`
	Summary         string                  `json:"summary"`
}
