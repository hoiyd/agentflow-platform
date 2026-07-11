package event

import (
	"context"
	"encoding/json"

	"agentflow-platform/apps/api/internal/domain"
)

type Sink interface {
	Publish(context.Context, domain.RunEvent) error
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

func Payload(value any) (map[string]any, error) {
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
