package store

import (
	"fmt"
	"scholar-agent-backend/internal/models"
	"sync"
)

// MemoryPlanStore is the first-phase in-memory implementation of PlanStore.
type MemoryPlanStore struct {
	mu     sync.RWMutex
	plans  map[string]*models.PlanGraph
	events map[string][]models.PlanEvent
}

func NewMemoryPlanStore() *MemoryPlanStore {
	return &MemoryPlanStore{
		plans:  map[string]*models.PlanGraph{},
		events: map[string][]models.PlanEvent{},
	}
}

func (s *MemoryPlanStore) SavePlan(plan *models.PlanGraph) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.ID] = clonePlanGraph(plan)
	if _, ok := s.events[plan.ID]; !ok {
		s.events[plan.ID] = []models.PlanEvent{}
	}
	return nil
}

func (s *MemoryPlanStore) GetPlan(planID string) (*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	plan, ok := s.plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	return clonePlanGraph(plan), nil
}

func (s *MemoryPlanStore) UpdatePlan(planID string, update func(*models.PlanGraph) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	plan, ok := s.plans[planID]
	if !ok {
		return fmt.Errorf("plan not found: %s", planID)
	}

	working := clonePlanGraph(plan)
	if err := update(working); err != nil {
		return err
	}

	s.plans[planID] = working
	return nil
}

func (s *MemoryPlanStore) AppendEvent(planID string, event models.PlanEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.plans[planID]; !ok {
		return fmt.Errorf("plan not found: %s", planID)
	}
	s.events[planID] = append(s.events[planID], event)
	return nil
}

func (s *MemoryPlanStore) ListEvents(planID string) ([]models.PlanEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, ok := s.plans[planID]; !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}

	events := s.events[planID]
	out := make([]models.PlanEvent, len(events))
	copy(out, events)
	return out, nil
}

func clonePlanGraph(plan *models.PlanGraph) *models.PlanGraph {
	if plan == nil {
		return nil
	}

	cloned := *plan
	cloned.Nodes = make([]*models.TaskNode, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		if node == nil {
			cloned.Nodes = append(cloned.Nodes, nil)
			continue
		}
		nodeCopy := *node
		if node.Dependencies != nil {
			nodeCopy.Dependencies = append([]string(nil), node.Dependencies...)
		}
		if node.RequiredArtifacts != nil {
			nodeCopy.RequiredArtifacts = append([]string(nil), node.RequiredArtifacts...)
		}
		if node.OutputArtifacts != nil {
			nodeCopy.OutputArtifacts = append([]string(nil), node.OutputArtifacts...)
		}
		if node.Inputs != nil {
			nodeCopy.Inputs = cloneStringAnyMap(node.Inputs)
		}
		if node.Metadata != nil {
			nodeCopy.Metadata = cloneStringAnyMap(node.Metadata)
		}
		cloned.Nodes = append(cloned.Nodes, &nodeCopy)
	}

	cloned.Edges = make([]*models.TaskEdge, 0, len(plan.Edges))
	for _, edge := range plan.Edges {
		if edge == nil {
			cloned.Edges = append(cloned.Edges, nil)
			continue
		}
		edgeCopy := *edge
		cloned.Edges = append(cloned.Edges, &edgeCopy)
	}

	cloned.Artifacts = make(map[string]models.Artifact, len(plan.Artifacts))
	for k, v := range plan.Artifacts {
		artifactCopy := v
		if v.Metadata != nil {
			artifactCopy.Metadata = cloneStringAnyMap(v.Metadata)
		}
		cloned.Artifacts[k] = artifactCopy
	}

	return &cloned
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
