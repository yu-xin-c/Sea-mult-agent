package models

import "time"

const TaskContractVersion = "v1"

// TaskContract is the versioned boundary shared by planners, agents and tools.
type TaskContract struct {
	Version         string   `json:"version"`
	InputArtifacts  []string `json:"input_artifacts"`
	OutputArtifacts []string `json:"output_artifacts"`
	AllowedTools    []string `json:"allowed_tools,omitempty"`
}

// ApprovalState records the human decision required before a high-risk plan runs.
type ApprovalState struct {
	Required   bool       `json:"required"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	ApprovedBy string     `json:"approved_by,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

// RunBudget contains enforceable limits for one plan execution.
type RunBudget struct {
	MaxTaskAttempts int `json:"max_task_attempts"`
	MaxDurationSec  int `json:"max_duration_seconds"`
}

// RunUsage is updated transactionally with task state.
type RunUsage struct {
	TaskAttempts int        `json:"task_attempts"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}
