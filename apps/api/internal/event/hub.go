package event

import (
	"context"
	"errors"
	"sync"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/eventcatalog"
)

var ErrSubscriberLagged = errors.New("event subscriber lagged; reconnect from the durable cursor")

type SubscriptionError struct {
	Code  string
	RunID string
	Cause error
}

func (e *SubscriptionError) Error() string { return e.Code + ": " + e.Cause.Error() }
func (e *SubscriptionError) Unwrap() error { return e.Cause }

type ProjectionLoader func() (domain.RunProjectionSnapshot, error)

type Subscription struct {
	Snapshot domain.RunProjectionSnapshot
	Events   <-chan domain.RunEvent
	Errors   <-chan error
	cancel   func()
}

func (s Subscription) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

type Hub struct {
	mu         sync.Mutex
	runs       map[string]*runSubscribers
	bufferSize int
}

type runSubscribers struct {
	mu     sync.Mutex
	nextID uint64
	items  map[uint64]*subscriber
}

type subscriber struct {
	afterSequence int64
	pending       map[int64]domain.RunEvent
	events        chan domain.RunEvent
	errors        chan error
}

func NewHub(bufferSize int) *Hub {
	if bufferSize <= 0 {
		bufferSize = 128
	}
	return &Hub{runs: map[string]*runSubscribers{}, bufferSize: bufferSize}
}

// SnapshotAndSubscribe closes the read/subscribe race by holding the per-Run
// publication gate while the durable snapshot is loaded and the subscriber is
// registered. Slow subscribers are disconnected instead of blocking execution;
// they resume from the last durable sequence.
func (h *Hub) SnapshotAndSubscribe(ctx context.Context, runID string, load ProjectionLoader) (Subscription, error) {
	if h == nil || load == nil {
		return Subscription{}, errors.New("event hub and projection loader are required")
	}
	state := h.run(runID)
	state.mu.Lock()
	snapshot, err := load()
	if err != nil {
		state.mu.Unlock()
		return Subscription{}, err
	}
	state.nextID++
	id := state.nextID
	item := &subscriber{
		afterSequence: snapshot.AsOfSequence,
		pending:       map[int64]domain.RunEvent{},
		events:        make(chan domain.RunEvent, h.bufferSize), errors: make(chan error, 1),
	}
	state.items[id] = item
	state.mu.Unlock()

	cancelOnce := sync.Once{}
	cancel := func() {
		cancelOnce.Do(func() { h.remove(runID, id, nil) })
	}
	if ctx != nil && ctx.Done() != nil {
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}
	return Subscription{Snapshot: snapshot, Events: item.events, Errors: item.errors, cancel: cancel}, nil
}

// PublishCommitted is called only after persistence succeeds, so subscribers
// never observe a durable fact that Replay cannot subsequently return.
func (h *Hub) PublishCommitted(item domain.RunEvent) {
	if h == nil || item.RunID == "" || item.Sequence <= 0 || !eventcatalog.IsDurable(item.Type) {
		return
	}
	h.publish(item, true)
}

func (h *Hub) PublishLive(item domain.RunEvent) {
	definition, ok := eventcatalog.DefinitionFor(item.Type)
	if h == nil || !ok || definition.Durability != eventcatalog.LiveOnly || item.RunID == "" {
		return
	}
	if err := eventcatalog.ValidateEnvelope(item); err != nil {
		return
	}
	h.publish(item, false)
}

func (h *Hub) publish(item domain.RunEvent, durable bool) {
	state := h.run(item.RunID)
	state.mu.Lock()
	defer state.mu.Unlock()
	for id, target := range state.items {
		if durable {
			if item.Sequence <= target.afterSequence {
				continue
			}
			target.pending[item.Sequence] = item
			if len(target.pending) > h.bufferSize {
				h.disconnectLagged(state, id, target, item.RunID)
				continue
			}
			for {
				nextSequence := target.afterSequence + 1
				next, ok := target.pending[nextSequence]
				if !ok {
					break
				}
				if !sendEvent(target, next) {
					h.disconnectLagged(state, id, target, item.RunID)
					break
				}
				delete(target.pending, nextSequence)
				target.afterSequence = nextSequence
			}
			continue
		}
		if !sendEvent(target, item) {
			h.disconnectLagged(state, id, target, item.RunID)
		}
	}
}

func sendEvent(target *subscriber, item domain.RunEvent) bool {
	select {
	case target.events <- item:
		return true
	default:
		return false
	}
}

func (h *Hub) disconnectLagged(state *runSubscribers, id uint64, target *subscriber, runID string) {
	target.errors <- &SubscriptionError{Code: "event_subscriber_lagged", RunID: runID, Cause: ErrSubscriberLagged}
	close(target.events)
	close(target.errors)
	delete(state.items, id)
}

func (h *Hub) run(runID string) *runSubscribers {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.runs[runID]
	if state == nil {
		state = &runSubscribers{items: map[uint64]*subscriber{}}
		h.runs[runID] = state
	}
	return state
}

func (h *Hub) remove(runID string, id uint64, reported error) {
	state := h.run(runID)
	state.mu.Lock()
	defer state.mu.Unlock()
	item, ok := state.items[id]
	if !ok {
		return
	}
	if reported != nil {
		item.errors <- reported
	}
	close(item.events)
	close(item.errors)
	delete(state.items, id)
}
