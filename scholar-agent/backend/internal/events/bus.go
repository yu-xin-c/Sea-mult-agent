package events

import (
	"scholar-agent-backend/internal/models"
	"sync"
)

// Bus is an in-memory pub/sub for plan execution events.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]map[chan models.PlanEvent]struct{}
}

func NewBus() *Bus {
	return &Bus{
		subscribers: map[string]map[chan models.PlanEvent]struct{}{},
	}
}

func (b *Bus) Publish(planID string, event models.PlanEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subscribers[planID] {
		select {
		case ch <- event:
		default:
		}
	}
}

func (b *Bus) Subscribe(planID string) chan models.PlanEvent {
	ch := make(chan models.PlanEvent, 64)

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subscribers[planID]; !ok {
		b.subscribers[planID] = map[chan models.PlanEvent]struct{}{}
	}
	b.subscribers[planID][ch] = struct{}{}
	return ch
}

func (b *Bus) Unsubscribe(planID string, ch chan models.PlanEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if subs, ok := b.subscribers[planID]; ok {
		if _, exists := subs[ch]; exists {
			delete(subs, ch)
			close(ch)
		}
		if len(subs) == 0 {
			delete(b.subscribers, planID)
		}
	}
}
