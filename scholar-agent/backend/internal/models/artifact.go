package models

import "time"

// Artifact is a structured output produced by a task and consumed by downstream tasks.
type Artifact struct {
	Key            string         `json:"key"`
	Type           string         `json:"type"`
	ProducerTaskID string         `json:"producer_task_id"`
	Value          string         `json:"value,omitempty"`
	Location       string         `json:"location,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}
