package scheduler

import "scholar-agent-backend/internal/models"

func shouldBlockOnFailure(task *models.TaskNode) bool {
	_ = task
	return true
}
