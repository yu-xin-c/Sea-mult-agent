package store

import "scholar-agent-backend/internal/models"

// PlanStore persists plan graphs and their event history.
type PlanStore interface {
	SavePlan(plan *models.PlanGraph) error
	GetPlan(planID string) (*models.PlanGraph, error)
	UpdatePlan(planID string, update func(*models.PlanGraph) error) error
	AppendEvent(planID string, event models.PlanEvent) error
	ListEvents(planID string) ([]models.PlanEvent, error)
}
