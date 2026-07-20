package models

// AblationBudget bounds the design search and the experiments selected from it.
type AblationBudget struct {
	MaxExperiments int `json:"max_experiments"`
	MaxGPUMinutes  int `json:"max_gpu_minutes"`
	MaxWallMinutes int `json:"max_wall_minutes"`
}

// AblationCandidate is one branch in the bounded experiment-design tree.
type AblationCandidate struct {
	ID                  string   `json:"id"`
	ParentID            string   `json:"parent_id,omitempty"`
	Category            string   `json:"category"`
	Title               string   `json:"title"`
	Hypothesis          string   `json:"hypothesis"`
	Change              string   `json:"change"`
	Metrics             []string `json:"metrics"`
	EstimatedMinutes    int      `json:"estimated_minutes"`
	EstimatedGPUMinutes int      `json:"estimated_gpu_minutes"`
	InformationGain     float64  `json:"information_gain"`
	Relevance           float64  `json:"relevance"`
	Reproducibility     float64  `json:"reproducibility"`
	Risk                float64  `json:"risk"`
	Score               float64  `json:"score"`
	EvaluationReason    string   `json:"evaluation_reason,omitempty"`
}

// AblationPlan records all explored branches and the budget-feasible selection.
type AblationPlan struct {
	Strategy        string              `json:"strategy"`
	MaxDepth        int                 `json:"max_depth"`
	BranchLimit     int                 `json:"branch_limit"`
	Budget          AblationBudget      `json:"budget"`
	Candidates      []AblationCandidate `json:"candidates"`
	Selected        []AblationCandidate `json:"selected"`
	PrunedIDs       []string            `json:"pruned_ids,omitempty"`
	SelectionReason string              `json:"selection_reason"`
}
