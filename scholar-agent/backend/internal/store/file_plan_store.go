package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"scholar-agent-backend/internal/models"
	"sync"
)

type fileSnapshot struct {
	Plans  map[string]*models.PlanGraph  `json:"plans"`
	Events map[string][]models.PlanEvent `json:"events"`
}

// FilePlanStore persists the complete scheduler state with atomic file replacement.
// It is intended for a reliable single-node deployment; clustered deployments should
// provide a transactional PlanStore backed by a shared database.
type FilePlanStore struct {
	mu     sync.RWMutex
	path   string
	plans  map[string]*models.PlanGraph
	events map[string][]models.PlanEvent
}

func NewFilePlanStore(path string) (*FilePlanStore, error) {
	if path == "" {
		return nil, fmt.Errorf("plan store path is empty")
	}
	store := &FilePlanStore{
		path:   path,
		plans:  map[string]*models.PlanGraph{},
		events: map[string][]models.PlanEvent{},
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plan store: %w", err)
	}
	if len(data) == 0 {
		return store, nil
	}
	var snapshot fileSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode plan store: %w", err)
	}
	if snapshot.Plans != nil {
		store.plans = snapshot.Plans
	}
	if snapshot.Events != nil {
		store.events = snapshot.Events
	}
	return store, nil
}

func (s *FilePlanStore) SavePlan(plan *models.PlanGraph) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.ID] = clonePlanGraph(plan)
	if _, ok := s.events[plan.ID]; !ok {
		s.events[plan.ID] = []models.PlanEvent{}
	}
	return s.persistLocked()
}

func (s *FilePlanStore) GetPlan(planID string) (*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plan, ok := s.plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	return clonePlanGraph(plan), nil
}

func (s *FilePlanStore) ListPlans() ([]*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plans := make([]*models.PlanGraph, 0, len(s.plans))
	for _, plan := range s.plans {
		plans = append(plans, clonePlanGraph(plan))
	}
	return plans, nil
}

func (s *FilePlanStore) UpdatePlan(planID string, update func(*models.PlanGraph) error) error {
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
	return s.persistLocked()
}

func (s *FilePlanStore) AppendEvent(planID string, event models.PlanEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[planID]; !ok {
		return fmt.Errorf("plan not found: %s", planID)
	}
	s.events[planID] = append(s.events[planID], clonePlanEvent(event))
	return s.persistLocked()
}

func (s *FilePlanStore) ListEvents(planID string) ([]models.PlanEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.plans[planID]; !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	events := s.events[planID]
	out := make([]models.PlanEvent, 0, len(events))
	for _, event := range events {
		out = append(out, clonePlanEvent(event))
	}
	return out, nil
}

func (s *FilePlanStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create plan store directory: %w", err)
	}
	data, err := json.MarshalIndent(fileSnapshot{Plans: s.plans, Events: s.events}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plan store: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".plans-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary plan store: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("replace plan store: %w", err)
	}
	directory, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return fmt.Errorf("open plan store directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync plan store directory: %w", err)
	}
	return nil
}
