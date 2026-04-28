package scheduler

import (
	"context"
	"fmt"
	"log"
	"scholar-agent-backend/internal/models"
	"scholar-agent-backend/internal/store"
	"sort"
	"time"
)

// EventPublisher emits incremental execution events.
type EventPublisher interface {
	Publish(planID string, event models.PlanEvent)
}

// NoopEventPublisher is used until the SSE/event bus layer is wired in.
type NoopEventPublisher struct{}

func (p *NoopEventPublisher) Publish(planID string, event models.PlanEvent) {
	_ = planID
	_ = event
}

// Scheduler executes one plan graph by continuously promoting and running ready nodes.
type Scheduler struct {
	store         store.PlanStore
	executor      TaskExecutor
	publisher     EventPublisher
	maxConcurrent int
	onTerminal    func(context.Context, *models.PlanGraph)
}

func NewScheduler(planStore store.PlanStore, executor TaskExecutor, publisher EventPublisher, maxConcurrent int) *Scheduler {
	if executor == nil {
		executor = NewDefaultTaskExecutor()
	}
	if publisher == nil {
		publisher = &NoopEventPublisher{}
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}

	return &Scheduler{
		store:         planStore,
		executor:      executor,
		publisher:     publisher,
		maxConcurrent: maxConcurrent,
	}
}

func (s *Scheduler) SetOnTerminal(fn func(context.Context, *models.PlanGraph)) {
	s.onTerminal = fn
}

func (s *Scheduler) ExecutePlan(ctx context.Context, planID string) error {
	if s.store == nil {
		return fmt.Errorf("plan store is nil")
	}

	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusInProgress {
			return fmt.Errorf("plan is already running")
		}
		plan.Status = models.StatusInProgress
		plan.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		return err
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_started",
		Timestamp: time.Now(),
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		plan, err := s.store.GetPlan(planID)
		if err != nil {
			return err
		}

		s.promotePendingNodesToReady(plan)
		if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
			copyPlanState(current, plan)
			fillMeta(current)
			return nil
		}); err != nil {
			return err
		}

		availableSlots := s.maxConcurrent - countInProgress(plan)
		ready := s.findReadyNodes(plan, availableSlots)
		if len(ready) == 0 {
			if allTerminal(plan) {
				finalStatus := models.StatusCompleted
				if hasFailure(plan) {
					finalStatus = models.StatusFailed
				}
				if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
					current.Status = finalStatus
					current.UpdatedAt = time.Now()
					fillMeta(current)
					return nil
				}); err != nil {
					return err
				}

				eventType := "plan_completed"
				if finalStatus == models.StatusFailed {
					eventType = "plan_failed"
				}
				s.publishAndStore(planID, models.PlanEvent{
					PlanID:    planID,
					EventType: eventType,
					Timestamp: time.Now(),
				})
				if s.onTerminal != nil {
					if latest, getErr := s.store.GetPlan(planID); getErr == nil && latest != nil {
						s.onTerminal(ctx, latest)
					} else {
						s.onTerminal(ctx, plan)
					}
				}
				return nil
			}

			if noRunning(plan) {
				if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
					current.Status = models.StatusFailed
					current.UpdatedAt = time.Now()
					fillMeta(current)
					return nil
				}); err != nil {
					return err
				}
				s.publishAndStore(planID, models.PlanEvent{
					PlanID:    planID,
					EventType: "plan_failed",
					Payload: map[string]any{
						"reason": "deadlock_or_unsatisfied_dependencies",
					},
					Timestamp: time.Now(),
				})
				if s.onTerminal != nil {
					if latest, getErr := s.store.GetPlan(planID); getErr == nil && latest != nil {
						s.onTerminal(ctx, latest)
					} else {
						s.onTerminal(ctx, plan)
					}
				}
				return nil
			}

			time.Sleep(200 * time.Millisecond)
			continue
		}

		if err := s.runReadyNodes(ctx, planID, ready); err != nil {
			return err
		}

		time.Sleep(150 * time.Millisecond)
	}
}

func (s *Scheduler) promotePendingNodesToReady(plan *models.PlanGraph) {
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusPending {
			continue
		}
		if allDependenciesCompleted(plan, node) && artifactsSatisfied(plan, node) {
			node.Status = models.StatusReady
			node.UpdatedAt = time.Now()
			s.publishAndStore(plan.ID, models.PlanEvent{
				PlanID:     plan.ID,
				EventType:  "task_ready",
				TaskID:     node.ID,
				TaskStatus: string(node.Status),
				Payload: map[string]any{
					"name": node.Name,
				},
				Timestamp: time.Now(),
			})
		}
	}
}

func (s *Scheduler) findReadyNodes(plan *models.PlanGraph, availableSlots int) []*models.TaskNode {
	if availableSlots <= 0 {
		return nil
	}

	ready := []*models.TaskNode{}
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusReady {
			ready = append(ready, node)
		}
	}

	if len(ready) == 0 {
		return nil
	}

	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})

	serial := make([]*models.TaskNode, 0, len(ready))
	parallel := make([]*models.TaskNode, 0, len(ready))
	for _, node := range ready {
		if node.Parallelizable {
			parallel = append(parallel, node)
		} else {
			serial = append(serial, node)
		}
	}

	if len(serial) > 0 {
		return serial[:1]
	}
	if len(parallel) > availableSlots {
		return parallel[:availableSlots]
	}
	return parallel
}

func (s *Scheduler) runReadyNodes(ctx context.Context, planID string, ready []*models.TaskNode) error {
	for _, task := range ready {
		if err := s.markTaskStarted(planID, task.ID); err != nil {
			return err
		}

		taskID := task.ID
		go func() {
			plan, err := s.store.GetPlan(planID)
			if err != nil {
				log.Printf("scheduler: failed to load plan %s for task %s: %v", planID, taskID, err)
				return
			}
			currentTask := getNodeByID(plan, taskID)
			if currentTask == nil {
				log.Printf("scheduler: task not found during execution: %s", taskID)
				return
			}

			logChannel := make(chan string, 64)
			execCtx := context.WithValue(ctx, "logChannel", logChannel)
			forwardDone := make(chan struct{})
			go s.forwardTaskLogs(planID, taskID, logChannel, forwardDone)

			result, execErr := s.executor.ExecuteTask(execCtx, plan, currentTask)
			close(logChannel)
			<-forwardDone

			if execErr != nil {
				result = &models.TaskExecutionResult{
					Status: models.StatusFailed,
					Error:  execErr.Error(),
				}
			}
			if result == nil {
				result = &models.TaskExecutionResult{
					Status: models.StatusFailed,
					Error:  "task execution returned nil result",
				}
			}

			if result.Status == models.StatusFailed {
				if err := s.markTaskFailed(planID, taskID, result); err != nil {
					log.Printf("scheduler: markTaskFailed failed for %s: %v", taskID, err)
					return
				}
				if shouldBlockOnFailure(currentTask) {
					if err := s.blockDependents(planID, taskID); err != nil {
						log.Printf("scheduler: blockDependents failed for %s: %v", taskID, err)
						return
					}
				}
				return
			}

			if err := s.markTaskCompleted(planID, taskID, result); err != nil {
				log.Printf("scheduler: markTaskCompleted failed for %s: %v", taskID, err)
				return
			}
		}()
	}
	return nil
}

func (s *Scheduler) forwardTaskLogs(planID, taskID string, logChannel <-chan string, done chan<- struct{}) {
	defer close(done)
	for line := range logChannel {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			EventType:  "task_log",
			TaskID:     taskID,
			TaskStatus: string(models.StatusInProgress),
			Payload: map[string]any{
				"message": line,
			},
			Timestamp: time.Now(),
		})
	}
}

func (s *Scheduler) markTaskStarted(planID, taskID string) error {
	now := time.Now()
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		task.Status = models.StatusInProgress
		task.StartedAt = &now
		task.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}

	s.publishAndStore(planID, models.PlanEvent{
		PlanID:     planID,
		EventType:  "task_started",
		TaskID:     taskID,
		TaskStatus: string(models.StatusInProgress),
		Timestamp:  now,
	})
	return nil
}

func (s *Scheduler) markTaskCompleted(planID, taskID string, result *models.TaskExecutionResult) error {
	now := time.Now()
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		task.Status = models.StatusCompleted
		task.Result = result.Result
		task.Code = result.Code
		task.ImageBase64 = result.ImageBase64
		task.Error = ""
		task.RunCount++
		task.FinishedAt = &now
		task.UpdatedAt = now

		for _, artifact := range result.Artifacts {
			plan.Artifacts[artifact.Key] = artifact
		}

		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}

	if len(result.Artifacts) > 0 {
		keys := make([]string, 0, len(result.Artifacts))
		for _, artifact := range result.Artifacts {
			keys = append(keys, artifact.Key)
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "artifact_created",
			TaskID:    taskID,
			Payload: map[string]any{
				"artifact_keys": keys,
			},
			Timestamp: now,
		})
	}

	s.publishAndStore(planID, models.PlanEvent{
		PlanID:     planID,
		EventType:  "task_completed",
		TaskID:     taskID,
		TaskStatus: string(models.StatusCompleted),
		Payload: map[string]any{
			"result_summary": result.Result,
			"result":         result.Result,
			"code":           result.Code,
			"image_base64":   result.ImageBase64,
		},
		Timestamp: now,
	})
	return nil
}

func (s *Scheduler) markTaskFailed(planID, taskID string, result *models.TaskExecutionResult) error {
	now := time.Now()
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		task.Status = models.StatusFailed
		task.Error = result.Error
		task.RunCount++
		task.FinishedAt = &now
		task.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}

	s.publishAndStore(planID, models.PlanEvent{
		PlanID:     planID,
		EventType:  "task_failed",
		TaskID:     taskID,
		TaskStatus: string(models.StatusFailed),
		Payload: map[string]any{
			"error": result.Error,
		},
		Timestamp: now,
	})
	return nil
}

func (s *Scheduler) blockDependents(planID, taskID string) error {
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		visited := map[string]bool{}
		var visit func(string)
		visit = func(currentID string) {
			for _, next := range getOutgoingNodes(plan, currentID) {
				if next == nil || visited[next.ID] {
					continue
				}
				visited[next.ID] = true
				if next.Status == models.StatusPending || next.Status == models.StatusReady {
					next.Status = models.StatusBlocked
					next.UpdatedAt = time.Now()
					events = append(events, models.PlanEvent{
						PlanID:     planID,
						EventType:  "task_blocked",
						TaskID:     next.ID,
						TaskStatus: string(models.StatusBlocked),
						Payload: map[string]any{
							"reason":           "upstream_failed",
							"upstream_task_id": currentID,
						},
						Timestamp: time.Now(),
					})
				}
				visit(next.ID)
			}
		}
		visit(taskID)
		fillMeta(plan)
		return nil
	})
	if err != nil {
		return err
	}
	if len(events) > 0 {
		for _, event := range events {
			s.publishAndStore(planID, event)
		}
	}
	return nil
}

func (s *Scheduler) publishAndStore(planID string, event models.PlanEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	_ = s.store.AppendEvent(planID, event)
	s.publisher.Publish(planID, event)
}

func allTerminal(plan *models.PlanGraph) bool {
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusPending, models.StatusReady, models.StatusInProgress:
			return false
		}
	}
	return true
}

func hasFailure(plan *models.PlanGraph) bool {
	for _, node := range plan.Nodes {
		if node != nil && (node.Status == models.StatusFailed || node.Status == models.StatusBlocked) {
			return true
		}
	}
	return false
}

func noRunning(plan *models.PlanGraph) bool {
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusInProgress {
			return false
		}
	}
	return true
}

func countInProgress(plan *models.PlanGraph) int {
	count := 0
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusInProgress {
			count++
		}
	}
	return count
}

func copyPlanState(dst, src *models.PlanGraph) {
	if dst == nil || src == nil {
		return
	}
	dst.Status = src.Status
	dst.Nodes = src.Nodes
	dst.Edges = src.Edges
	dst.Artifacts = src.Artifacts
	dst.Meta = src.Meta
	dst.UpdatedAt = src.UpdatedAt
}

func fillMeta(plan *models.PlanGraph) {
	meta := models.GraphMeta{
		TotalNodes: len(plan.Nodes),
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusCompleted:
			meta.CompletedNodes++
		case models.StatusFailed:
			meta.FailedNodes++
		case models.StatusBlocked:
			meta.BlockedNodes++
		case models.StatusInProgress:
			meta.InProgressNodes++
		case models.StatusReady:
			meta.ReadyNodes++
		}
	}
	plan.Meta = meta
}
