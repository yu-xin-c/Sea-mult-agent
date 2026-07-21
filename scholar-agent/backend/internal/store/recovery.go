package store

import (
	"scholar-agent-backend/internal/models"
	"time"
)

// RecoverInterruptedPlans invalidates stale leases and makes interrupted work runnable again.
func RecoverInterruptedPlans(planStore PlanStore) error {
	plans, err := planStore.ListPlans()
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if plan == nil || plan.Status != models.StatusInProgress {
			continue
		}
		if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
			current.Status = models.StatusPending
			current.UpdatedAt = time.Now()
			for _, node := range current.Nodes {
				if node == nil || node.Status != models.StatusInProgress {
					continue
				}
				node.Status = models.StatusPending
				node.ExecutionID = ""
				node.LeaseOwner = ""
				node.LeaseExpiresAt = nil
				node.UpdatedAt = time.Now()
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
