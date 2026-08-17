package models

import "time"

const (
	ExperimentDatasetVersion    = "experiment.dataset/v1"
	ExperimentSpecVersion       = "experiment.spec/v1"
	ExperimentLedgerVersion     = "experiment.ledger/v1"
	ExperimentValidationVersion = "experiment.validation/v1"
	ExperimentEvaluationVersion = "experiment.evaluation/v1"
	ExperimentFeatureVersion    = "experiment.features/v1"
	ExperimentDecisionVersion   = "experiment.policy-decision/v1"
	ExperimentOutcomeVersion    = "experiment.experience-outcome/v1"
	ExperimentRewardVersion     = "experiment.reward/v1"
	ExperimentExecutionSerial   = "serial/v1"
	ExperimentExecutionReadOnly = "shared-readonly/v1"
)

// ExperimentDatasetFeatureProfile is the stable context supplied to a search
// policy. Numeric and boolean maps keep the core protocol domain-neutral while
// the extractor name makes feature evolution explicit and reproducible.
type ExperimentDatasetFeatureProfile struct {
	Version            string             `json:"version"`
	ID                 string             `json:"id"`
	Extractor          string             `json:"extractor"`
	Domain             string             `json:"domain"`
	Adapter            string             `json:"adapter"`
	DatasetFingerprint string             `json:"dataset_fingerprint"`
	Numeric            map[string]float64 `json:"numeric"`
	Boolean            map[string]bool    `json:"boolean"`
	CreatedAt          time.Time          `json:"created_at"`
}

// ExperimentDatasetManifest is the domain-neutral result of adapting uploaded
// research data. Asset meanings are declared by the adapter instead of being
// hard-coded into the AutoResearch harness.
type ExperimentDatasetManifest struct {
	Version        string                           `json:"version"`
	Name           string                           `json:"name"`
	Domain         string                           `json:"domain"`
	Adapter        string                           `json:"adapter"`
	Mapping        map[string]string                `json:"mapping"`
	Counts         map[string]int                   `json:"counts"`
	Capabilities   map[string]bool                  `json:"capabilities"`
	Assets         map[string]string                `json:"assets"`
	SplitMethod    string                           `json:"split_method"`
	SourceFiles    []ResearchFileHash               `json:"source_files"`
	FrozenFiles    []ResearchFileHash               `json:"frozen_files"`
	FeatureProfile *ExperimentDatasetFeatureProfile `json:"feature_profile,omitempty"`
	CreatedAt      time.Time                        `json:"created_at"`
}

// ExperimentRewardSpec freezes how heterogeneous metrics and resource cost are
// converted into a cross-task learning reward. Search acceptance still uses the
// original metric and min_delta; reward never controls correctness by itself.
type ExperimentRewardSpec struct {
	Version                  string  `json:"version"`
	QualityTransform         string  `json:"quality_transform"`
	DurationPenaltyPerSecond float64 `json:"duration_penalty_per_second"`
	FailurePenalty           float64 `json:"failure_penalty"`
}

// ExperimentParameter defines a finite, auditable parameter domain. Restricting
// search to declared values keeps generated candidates inside the frozen budget.
type ExperimentParameter struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Values      []any  `json:"values"`
	Default     any    `json:"default"`
}

// ExperimentStrategy describes one method/module branch and its local search
// dimensions. Comparing strategy branches is the module-level ablation; moving
// between parameter values is the hyperparameter-level ablation.
type ExperimentStrategy struct {
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Parameters  []ExperimentParameter `json:"parameters"`
}

// ExperimentResearchSpec is the immutable contract for a configuration-search
// campaign. Commands are argv arrays and may only substitute {config_path}.
type ExperimentResearchSpec struct {
	Version             string                           `json:"version"`
	Name                string                           `json:"name"`
	Domain              string                           `json:"domain"`
	Adapter             string                           `json:"adapter"`
	CandidateKind       string                           `json:"candidate_kind"`
	Objective           string                           `json:"objective"`
	SearchCommand       []string                         `json:"search_command"`
	HoldoutCommand      []string                         `json:"holdout_command"`
	Strategies          []ExperimentStrategy             `json:"strategies"`
	MetricKey           string                           `json:"metric_key"`
	Direction           string                           `json:"direction"`
	MinDelta            float64                          `json:"min_delta"`
	TargetScore         *float64                         `json:"target_score,omitempty"`
	HoldoutTargetScore  *float64                         `json:"holdout_target_score,omitempty"`
	MaxTrials           int                              `json:"max_trials"`
	MaxParallelTrials   int                              `json:"max_parallel_trials"`
	EvaluationIsolation string                           `json:"evaluation_isolation"`
	MaxWallSeconds      int                              `json:"max_wall_seconds"`
	ValidationRuns      int                              `json:"validation_runs"`
	ContextProfile      *ExperimentDatasetFeatureProfile `json:"context_profile,omitempty"`
	RewardSpec          ExperimentRewardSpec             `json:"reward_spec"`
	Dependencies        []string                         `json:"dependencies"`
	FrozenFiles         []ResearchFileHash               `json:"frozen_files"`
	CreatedAt           time.Time                        `json:"created_at"`
}

// ExperimentPolicyDecision records what was available before an evaluator was
// run. Propensity is required for later off-policy analysis; fallback decisions
// remain visible instead of being presented as learned-policy choices.
type ExperimentPolicyDecision struct {
	Version               string    `json:"version"`
	CampaignID            string    `json:"campaign_id"`
	TrialNumber           int       `json:"trial_number"`
	PolicyVersion         string    `json:"policy_version"`
	CandidateID           string    `json:"candidate_id"`
	AvailableCandidateIDs []string  `json:"available_candidate_ids"`
	Propensity            float64   `json:"propensity"`
	PredictedReward       *float64  `json:"predicted_reward,omitempty"`
	ReasonCodes           []string  `json:"reason_codes,omitempty"`
	Fallback              bool      `json:"fallback"`
	SelectedAt            time.Time `json:"selected_at"`
}

// ExperimentCandidate is one node in the result-conditioned search tree.
type ExperimentCandidate struct {
	ID               string         `json:"id"`
	ParentID         string         `json:"parent_id,omitempty"`
	Strategy         string         `json:"strategy"`
	Parameters       map[string]any `json:"parameters"`
	Depth            int            `json:"depth"`
	ChangedParameter string         `json:"changed_parameter,omitempty"`
	Reason           string         `json:"reason"`
}

// ExperimentCaseEvidence is a bounded, adapter-defined explanation record.
// Expected/Observed work for retrieval IDs, labels, generated answers or any
// other discrete evidence; Details carries non-sensitive adapter annotations.
type ExperimentCaseEvidence struct {
	CaseID   string             `json:"case_id"`
	Expected []string           `json:"expected,omitempty"`
	Observed []string           `json:"observed,omitempty"`
	Metrics  map[string]float64 `json:"metrics,omitempty"`
	Details  map[string]any     `json:"details,omitempty"`
}

// ExperimentEvaluation is the required stdout contract for every adapter.
type ExperimentEvaluation struct {
	Version     string                   `json:"version"`
	CandidateID string                   `json:"candidate_id"`
	Strategy    string                   `json:"strategy"`
	Parameters  map[string]any           `json:"parameters"`
	Metrics     map[string]float64       `json:"metrics"`
	CaseCount   int                      `json:"case_count"`
	AssetHashes map[string]string        `json:"asset_hashes"`
	Evidence    []ExperimentCaseEvidence `json:"evidence,omitempty"`
}

// ExperimentTrial records why a candidate existed, what it scored and whether
// the deterministic harness retained it.
type ExperimentTrial struct {
	Number         int                       `json:"number"`
	Batch          int                       `json:"batch"`
	Worker         int                       `json:"worker"`
	Candidate      ExperimentCandidate       `json:"candidate"`
	Status         string                    `json:"status"`
	Decision       string                    `json:"decision"`
	Reason         string                    `json:"reason"`
	Metrics        map[string]float64        `json:"metrics,omitempty"`
	Score          *float64                  `json:"score,omitempty"`
	DeltaFromBest  *float64                  `json:"delta_from_best,omitempty"`
	Reward         *float64                  `json:"reward,omitempty"`
	PolicyDecision *ExperimentPolicyDecision `json:"policy_decision,omitempty"`
	DurationMS     int64                     `json:"duration_ms"`
	Error          string                    `json:"error,omitempty"`
	StartedAt      time.Time                 `json:"started_at"`
	FinishedAt     time.Time                 `json:"finished_at"`
}

// ExperimentExperienceOutcome is sent to the append-only experience service
// after a real evaluator run. Failed and rejected actions are retained because
// omitting them would create survivorship bias in future policies.
type ExperimentExperienceOutcome struct {
	Version           string              `json:"version"`
	CampaignID        string              `json:"campaign_id"`
	TrialNumber       int                 `json:"trial_number"`
	ContextID         string              `json:"context_id,omitempty"`
	Candidate         ExperimentCandidate `json:"candidate"`
	Status            string              `json:"status"`
	BaselineScore     float64             `json:"baseline_score"`
	CandidateScore    *float64            `json:"candidate_score,omitempty"`
	DeltaFromBaseline *float64            `json:"delta_from_baseline,omitempty"`
	Reward            float64             `json:"reward"`
	DurationMS        int64               `json:"duration_ms"`
	Error             string              `json:"error,omitempty"`
	RecordedAt        time.Time           `json:"recorded_at"`
}

type ExperimentResourceUsage struct {
	EvaluatorRuns   int   `json:"evaluator_runs"`
	EvaluatorTimeMS int64 `json:"evaluator_time_ms"`
	WallDurationMS  int64 `json:"wall_duration_ms"`
	PeakParallelism int   `json:"peak_parallelism"`
}

// ExperimentTrialLedger is the append-only evidence record for a campaign.
type ExperimentTrialLedger struct {
	Version             string                  `json:"version"`
	CampaignID          string                  `json:"campaign_id,omitempty"`
	SpecSHA256          string                  `json:"spec_sha256"`
	Status              string                  `json:"status"`
	Domain              string                  `json:"domain"`
	Adapter             string                  `json:"adapter"`
	MetricKey           string                  `json:"metric_key"`
	Direction           string                  `json:"direction"`
	TargetScore         *float64                `json:"target_score,omitempty"`
	BaselineScore       float64                 `json:"baseline_score"`
	BestScore           float64                 `json:"best_score"`
	MaxTrials           int                     `json:"max_trials"`
	MaxParallelTrials   int                     `json:"max_parallel_trials"`
	EvaluationIsolation string                  `json:"evaluation_isolation"`
	AblationPlanSHA256  string                  `json:"ablation_plan_sha256,omitempty"`
	DesignedBranches    int                     `json:"designed_branches,omitempty"`
	CompletedTrials     int                     `json:"completed_trials"`
	AcceptedTrials      int                     `json:"accepted_trials"`
	StrategySpace       []ExperimentStrategy    `json:"strategy_space,omitempty"`
	Trials              []ExperimentTrial       `json:"trials"`
	BestCandidate       ExperimentCandidate     `json:"best_candidate"`
	BestEvaluation      ExperimentEvaluation    `json:"best_evaluation"`
	StopReason          string                  `json:"stop_reason"`
	ResourceUsage       ExperimentResourceUsage `json:"resource_usage"`
	StartedAt           time.Time               `json:"started_at"`
	FinishedAt          time.Time               `json:"finished_at"`
}

type ExperimentBestCandidate struct {
	SpecSHA256 string               `json:"spec_sha256"`
	Score      float64              `json:"score"`
	MetricKey  string               `json:"metric_key"`
	Candidate  ExperimentCandidate  `json:"candidate"`
	Evaluation ExperimentEvaluation `json:"evaluation"`
}

type ExperimentValidationRun struct {
	Number         int                      `json:"number"`
	Status         string                   `json:"status"`
	BaselineScore  float64                  `json:"baseline_score"`
	CandidateScore float64                  `json:"candidate_score"`
	Delta          float64                  `json:"delta"`
	TargetReached  bool                     `json:"target_reached"`
	DurationMS     int64                    `json:"duration_ms"`
	Evidence       []ExperimentCaseEvidence `json:"evidence,omitempty"`
	Error          string                   `json:"error,omitempty"`
}

// ExperimentValidationReport keeps tuned search evidence separate from fresh
// holdout acceptance, preventing a search score from being presented as proof.
type ExperimentValidationReport struct {
	Version         string                    `json:"version"`
	Status          string                    `json:"status"`
	SpecSHA256      string                    `json:"spec_sha256"`
	LedgerSHA256    string                    `json:"ledger_sha256"`
	Domain          string                    `json:"domain"`
	Adapter         string                    `json:"adapter"`
	MetricKey       string                    `json:"metric_key"`
	SearchBaseline  float64                   `json:"search_baseline"`
	SearchBest      float64                   `json:"search_best"`
	HoldoutTarget   *float64                  `json:"holdout_target,omitempty"`
	RequestedRuns   int                       `json:"requested_runs"`
	PassedRuns      int                       `json:"passed_runs"`
	ProtectedIntact bool                      `json:"protected_intact"`
	Runs            []ExperimentValidationRun `json:"runs"`
	Summary         string                    `json:"summary"`
	ValidatedAt     time.Time                 `json:"validated_at"`
}
