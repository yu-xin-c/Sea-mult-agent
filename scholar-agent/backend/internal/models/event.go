package models

import "time"

// PlanEvent is an incremental event emitted while a plan is being executed.
type PlanEvent struct {
	PlanID     string         `json:"plan_id"`
	EventType  string         `json:"event_type"`
	TaskID     string         `json:"task_id,omitempty"`
	TaskStatus string         `json:"task_status,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
	Timestamp  time.Time      `json:"timestamp"`
}
