package event

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/eventcatalog"
)

type Sink interface {
	Publish(context.Context, domain.RunEvent) error
}

type RunEventStore interface {
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}
type LivePublisher interface {
	PublishLive(domain.RunEvent)
}

type StoreSink struct {
	Store RunEventStore
	Live  LivePublisher
}

func (s StoreSink) Publish(_ context.Context, event domain.RunEvent) error {
	definition, ok := eventcatalog.DefinitionFor(event.Type)
	if !ok {
		return fmt.Errorf("run event type %q is not registered", event.Type)
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = definition.SchemaVersion
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if err := eventcatalog.ValidateEnvelope(event); err != nil {
		return err
	}
	if definition.Durability == eventcatalog.LiveOnly {
		if s.Live != nil {
			s.Live.PublishLive(event)
		}
		return nil
	}
	if s.Store == nil {
		return nil
	}
	_, err := s.Store.CreateRunEvent(event)
	return err
}

type SinkFunc func(context.Context, domain.RunEvent) error

func (f SinkFunc) Publish(ctx context.Context, event domain.RunEvent) error {
	return f(ctx, event)
}

type MultiSink []Sink

func (s MultiSink) Publish(ctx context.Context, event domain.RunEvent) error {
	for _, sink := range s {
		if sink == nil {
			continue
		}
		if err := sink.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func Payload(value PayloadContract) (map[string]any, error) {
	if trace, ok := value.(TracePayload); ok {
		return sanitizePayload(trace.Fields), nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
