package models

import "time"

// TaskStatus represents the current state of a task
type TaskStatus string

const (
	StatusPending    TaskStatus = "pending"
	StatusInProgress TaskStatus = "in_progress"
	StatusCompleted  TaskStatus = "completed"
	StatusFailed     TaskStatus = "failed"
)

// Task represents a single node in the execution DAG
type Task struct {
	ID           string
	Name         string
	Description  string
	AssignedTo   string // e.g., "librarian_agent", "coder_agent"
	Status       TaskStatus
	Dependencies []string // IDs of tasks that must complete before this one
	Result       string   // Output of the task
	Code         string   // Generated code for the task
	ImageBase64  string   // Base64 encoded image if the task generated a plot
	Error        string   // Error message if failed
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Plan represents the overall DAG generated for a user intent
type Plan struct {
	ID         string
	UserIntent string
	Tasks      map[string]*Task
	Status     TaskStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
