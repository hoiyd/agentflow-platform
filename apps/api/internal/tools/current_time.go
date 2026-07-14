package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

func CurrentTimeTool() Binding {
	return Binding{
		Descriptor: Descriptor{
			Name:        "get_current_time",
			Description: "Return the current time for an IANA timezone.",
			Concurrency: ConcurrencyPolicy{Mode: ConcurrencyReadOnly},
			Parameters: ObjectSchema(map[string]any{
				"timezone": map[string]any{
					"type":        "string",
					"description": "IANA timezone such as Asia/Shanghai or America/Los_Angeles.",
				},
			}, []string{"timezone"}),
		},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Timezone string `json:"timezone"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid time arguments: %w", err)
			}
			if input.Timezone == "" {
				input.Timezone = "UTC"
			}
			location, err := time.LoadLocation(input.Timezone)
			if err != nil {
				return nil, fmt.Errorf("invalid timezone %q: %w", input.Timezone, err)
			}
			now := time.Now().In(location)
			return map[string]any{
				"timezone": input.Timezone,
				"iso":      now.Format(time.RFC3339),
				"display":  now.Format("2006-01-02 15:04:05 MST"),
			}, nil
		},
	}
}
