package scheduler

import (
	"context"
	"fmt"
	"scholar-agent-backend/internal/models"
	"strings"
	"time"
)

// TaskExecutor executes one task node for the scheduler.
type TaskExecutor interface {
	ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error)
}

type AgentRunner interface {
	ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error
}

// DefaultTaskExecutor remains as a lightweight fallback when no real agent router is injected.
type DefaultTaskExecutor struct{}

func NewDefaultTaskExecutor() *DefaultTaskExecutor {
	return &DefaultTaskExecutor{}
}

func (e *DefaultTaskExecutor) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	_ = ctx
	_ = plan
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}

	return &models.TaskExecutionResult{
		Status:    models.StatusCompleted,
		Result:    fmt.Sprintf("task %s executed by placeholder executor", task.Name),
		Artifacts: buildArtifacts(task, &models.Task{Result: fmt.Sprintf("task %s executed by placeholder executor", task.Name)}),
	}, nil
}

// RoutedTaskExecutor dispatches plan nodes to the existing specialized agents.
type RoutedTaskExecutor struct {
	Librarian AgentRunner
	Data      AgentRunner
	Coder     AgentRunner
}

func NewRoutedTaskExecutor(librarian, data, coder AgentRunner) *RoutedTaskExecutor {
	return &RoutedTaskExecutor{
		Librarian: librarian,
		Data:      data,
		Coder:     coder,
	}
}

func (e *RoutedTaskExecutor) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}

	runtimeTask := &models.Task{
		ID:                task.ID,
		Name:              task.Name,
		Type:              task.Type,
		Description:       buildTaskDescription(plan, task),
		AssignedTo:        task.AssignedTo,
		Status:            models.StatusPending,
		Dependencies:      append([]string(nil), task.Dependencies...),
		RequiredArtifacts: append([]string(nil), task.RequiredArtifacts...),
		OutputArtifacts:   append([]string(nil), task.OutputArtifacts...),
		Parallelizable:    task.Parallelizable,
		Priority:          task.Priority,
		RetryLimit:        task.RetryLimit,
		RunCount:          task.RunCount,
		Inputs:            buildTaskInputs(plan, task),
		Metadata: map[string]any{
			"plan_id": plan.ID,
		},
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
	}

	sharedContext := map[string]interface{}{
		"plan_id":       plan.ID,
		"user_intent":   plan.UserIntent,
		"intent_type":   plan.IntentType,
		"task_type":     task.Type,
		"artifact_keys": append([]string(nil), task.RequiredArtifacts...),
	}

	runner, err := e.resolveRunner(task.AssignedTo)
	if err != nil {
		return nil, err
	}
	if err := runner.ExecuteTask(ctx, runtimeTask, sharedContext); err != nil {
		status := runtimeTask.Status
		if status == "" {
			status = models.StatusFailed
		}
		return &models.TaskExecutionResult{
			Status: status,
			Result: runtimeTask.Result,
			Code:   runtimeTask.Code,
			Error:  chooseNonEmpty(runtimeTask.Error, err.Error()),
		}, nil
	}

	status := runtimeTask.Status
	if status == "" || status == models.StatusPending {
		status = models.StatusCompleted
	}

	return &models.TaskExecutionResult{
		Status:      status,
		Result:      runtimeTask.Result,
		Code:        runtimeTask.Code,
		ImageBase64: runtimeTask.ImageBase64,
		Error:       runtimeTask.Error,
		Artifacts:   buildArtifacts(task, runtimeTask),
	}, nil
}

func (e *RoutedTaskExecutor) resolveRunner(assignedTo string) (AgentRunner, error) {
	switch assignedTo {
	case "librarian_agent":
		if e.Librarian != nil {
			return e.Librarian, nil
		}
	case "data_agent":
		if e.Data != nil {
			return e.Data, nil
		}
	case "coder_agent", "sandbox_agent", "general_agent":
		if e.Coder != nil {
			return e.Coder, nil
		}
	}
	return nil, fmt.Errorf("no agent runner configured for %s", assignedTo)
}

func buildTaskInputs(plan *models.PlanGraph, task *models.TaskNode) map[string]any {
	inputs := map[string]any{}
	if plan == nil || task == nil {
		return inputs
	}

	for _, key := range task.RequiredArtifacts {
		if artifact, ok := plan.Artifacts[key]; ok {
			inputs[key] = artifact.Value
		}
	}
	return inputs
}

func buildTaskDescription(plan *models.PlanGraph, task *models.TaskNode) string {
	if task == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(task.Description)

	if plan != nil {
		builder.WriteString("\n\nGlobal user intent:\n")
		builder.WriteString(plan.UserIntent)
		builder.WriteString("\n\nPlan intent type: ")
		builder.WriteString(plan.IntentType)
	}

	if len(task.RequiredArtifacts) > 0 && plan != nil {
		builder.WriteString("\n\nAvailable upstream artifacts:\n")
		for _, key := range task.RequiredArtifacts {
			artifact, ok := plan.Artifacts[key]
			if !ok {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(truncateArtifactValue(artifact.Value))
			builder.WriteString("\n")
		}
	}

	if len(task.OutputArtifacts) > 0 {
		builder.WriteString("\nExpected outputs:\n")
		for _, key := range task.OutputArtifacts {
			builder.WriteString("- ")
			builder.WriteString(key)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

func buildArtifacts(node *models.TaskNode, runtimeTask *models.Task) []models.Artifact {
	if node == nil || runtimeTask == nil {
		return nil
	}

	artifacts := make([]models.Artifact, 0, len(node.OutputArtifacts))
	for _, key := range node.OutputArtifacts {
		value, artifactType := resolveArtifactPayload(key, runtimeTask)
		artifacts = append(artifacts, models.Artifact{
			Key:            key,
			Type:           artifactType,
			ProducerTaskID: node.ID,
			Value:          value,
			Metadata: map[string]any{
				"task_name":    node.Name,
				"assigned_to":  node.AssignedTo,
				"source_type":  node.Type,
				"has_code":     runtimeTask.Code != "",
				"has_image":    runtimeTask.ImageBase64 != "",
				"result_error": runtimeTask.Error,
			},
			CreatedAt: time.Now(),
		})
	}
	return artifacts
}

func resolveArtifactPayload(key string, runtimeTask *models.Task) (string, string) {
	if explicit, ok := explicitArtifactValue(runtimeTask, key); ok {
		return explicit, inferArtifactType(key, runtimeTask)
	}

	return inferArtifactValue(key, runtimeTask), inferArtifactType(key, runtimeTask)
}

func explicitArtifactValue(runtimeTask *models.Task, key string) (string, bool) {
	if runtimeTask == nil || runtimeTask.Metadata == nil {
		return "", false
	}

	rawMap, ok := runtimeTask.Metadata["artifact_values"].(map[string]string)
	if ok {
		value, exists := rawMap[key]
		return value, exists
	}

	anyMap, ok := runtimeTask.Metadata["artifact_values"].(map[string]any)
	if ok {
		value, exists := anyMap[key]
		if !exists || value == nil {
			return "", false
		}
		return fmt.Sprint(value), true
	}

	return "", false
}

func inferArtifactValue(key string, runtimeTask *models.Task) string {
	lowerKey := strings.ToLower(key)
	switch {
	case strings.Contains(lowerKey, "plot") || strings.Contains(lowerKey, "image"):
		return chooseNonEmpty(runtimeTask.ImageBase64, runtimeTask.Result)
	case strings.Contains(lowerKey, "code"):
		return chooseNonEmpty(runtimeTask.Code, runtimeTask.Result)
	case strings.Contains(lowerKey, "url"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	case strings.Contains(lowerKey, "path") || strings.Contains(lowerKey, "env") || strings.Contains(lowerKey, "workspace") || strings.Contains(lowerKey, "runtime"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	case strings.Contains(lowerKey, "metrics"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	case strings.Contains(lowerKey, "report"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	default:
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code, runtimeTask.ImageBase64, nodeArtifactFallback(key))
	}
}

func inferArtifactType(key string, runtimeTask *models.Task) string {
	_ = runtimeTask
	lowerKey := strings.ToLower(key)
	switch {
	case strings.Contains(lowerKey, "plot") || strings.Contains(lowerKey, "image"):
		return "image_base64"
	case strings.Contains(lowerKey, "code"):
		return "code"
	case strings.Contains(lowerKey, "url"):
		return "url"
	case strings.Contains(lowerKey, "path") || strings.Contains(lowerKey, "env") || strings.Contains(lowerKey, "workspace") || strings.Contains(lowerKey, "runtime"):
		return "text"
	case strings.Contains(lowerKey, "metrics"):
		return "metrics"
	case strings.Contains(lowerKey, "report"):
		return "report"
	case strings.Contains(lowerKey, "dependency"):
		return "dependency_spec"
	default:
		return "text"
	}
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateArtifactValue(value string) string {
	const limit = 500
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func nodeArtifactFallback(key string) string {
	return fmt.Sprintf("artifact generated for %s", key)
}
