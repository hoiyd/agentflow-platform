package telemetry

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type limits struct {
	queue, active int
	ttl, sweep    time.Duration
}

func defaultLimits() limits {
	return limits{queue: 1024, active: 2048, ttl: time.Hour, sweep: time.Minute}
}

type activeSpan struct {
	ctx    context.Context
	span   trace.Span
	runID  string
	opened time.Time
}

// Observer owns one bounded metadata queue and one worker; Observe never waits
// for an exporter. The provider is local, not the global OpenTelemetry provider.
type Observer struct {
	mu      sync.RWMutex
	closed  bool
	queue   chan boundary
	done    chan struct{}
	dropped atomic.Uint64
}

func newObserver(provider *sdktrace.TracerProvider, bounds limits) *Observer {
	o := &Observer{queue: make(chan boundary, bounds.queue), done: make(chan struct{})}
	go o.consume(provider, bounds)
	return o
}

func (o *Observer) Observe(event domain.RunEvent) {
	if o == nil {
		return
	}
	item, ok := project(event)
	if !ok {
		return
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.closed {
		return
	}
	select {
	case o.queue <- item:
	default:
		o.dropped.Add(1)
	}
}
func (o *Observer) Dropped() uint64 {
	if o == nil {
		return 0
	}
	return o.dropped.Load()
}
func (o *Observer) Shutdown(ctx context.Context) error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		close(o.queue)
	}
	o.mu.Unlock()
	select {
	case <-o.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (o *Observer) consume(provider *sdktrace.TracerProvider, bounds limits) {
	defer close(o.done)
	active := make(map[string]activeSpan)
	tracer := provider.Tracer("agentflow/committed-events")
	finish := func(key string, at time.Time, attrs []attribute.KeyValue, status string) {
		s, ok := active[key]
		if !ok {
			return
		}
		s.span.SetAttributes(attrs...)
		if status == "failed" || status == "incomplete" {
			s.span.SetStatus(codes.Error, status)
		}
		s.span.End(trace.WithTimestamp(at))
		delete(active, key)
	}
	finishRun := func(runID string, at time.Time, attrs []attribute.KeyValue, status string) {
		for key, s := range active {
			if s.runID == runID && key != runID+"/run" {
				finish(key, at, []attribute.KeyValue{attribute.String("agentflow.trace.incomplete_reason", "parent_closed")}, "incomplete")
			}
		}
		finish(runID+"/run", at, attrs, status)
	}
	ticker := time.NewTicker(bounds.sweep)
	defer ticker.Stop()
	for {
		select {
		case item, ok := <-o.queue:
			if !ok {
				for key := range active {
					finish(key, time.Now(), []attribute.KeyValue{attribute.String("agentflow.trace.incomplete_reason", "shutdown")}, "incomplete")
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				if err := provider.Shutdown(ctx); err != nil {
					log.Print("OpenTelemetry shutdown incomplete; execution is unaffected")
				}
				cancel()
				if dropped := o.Dropped(); dropped > 0 {
					log.Printf("OpenTelemetry dropped %d boundaries (queue/cap/missing parent); Replay remains authoritative", dropped)
				}
				return
			}
			if !item.start {
				if item.name == "agentflow.run" {
					finishRun(item.runID, item.at, item.attrs, item.status)
				} else {
					finish(item.key, item.at, item.attrs, item.status)
				}
				continue
			}
			if _, exists := active[item.key]; exists {
				continue
			}
			if len(active) >= bounds.active {
				o.dropped.Add(1)
				continue
			}
			ctx := context.Background()
			if item.name != "agentflow.run" {
				parent, exists := active[item.parent]
				if !exists {
					item.attrs = append(item.attrs, attribute.Bool("agentflow.trace.parent_boundary_missing", true))
					parent, exists = active[item.stageParent]
				}
				if !exists {
					parent, exists = active[item.runID+"/run"]
				}
				if !exists {
					o.dropped.Add(1)
					continue
				}
				ctx = parent.ctx
			}
			ctx, span := tracer.Start(ctx, item.name, trace.WithTimestamp(item.at), trace.WithAttributes(item.attrs...))
			active[item.key] = activeSpan{ctx: ctx, span: span, runID: item.runID, opened: time.Now()}
		case now := <-ticker.C:
			for key, s := range active {
				if now.Sub(s.opened) >= bounds.ttl {
					attrs := []attribute.KeyValue{attribute.String("agentflow.trace.incomplete_reason", "expired")}
					if key == s.runID+"/run" {
						finishRun(s.runID, now, attrs, "incomplete")
					} else {
						finish(key, now, attrs, "incomplete")
					}
				}
			}
		}
	}
}
