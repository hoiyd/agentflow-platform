package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func MockWebSearchTool() Tool {
	return Tool{
		Name:        "mock_web_search",
		Description: "Return deterministic mock search results for local development without external browsing.",
		Parameters: ObjectSchema(map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query.",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return.",
			},
		}, []string{"query"}),
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			var input struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, fmt.Errorf("invalid search arguments: %w", err)
			}
			input.Query = strings.TrimSpace(input.Query)
			if input.Query == "" {
				return nil, fmt.Errorf("query is required")
			}
			if input.Limit <= 0 || input.Limit > 5 {
				input.Limit = 3
			}
			results := []map[string]string{
				{
					"title":   "AgentFlow internal note",
					"url":     "mock://search/agentflow-platform",
					"snippet": "AgentFlow supports chat, tool calling, workflow orchestration, and execution tracing.",
				},
				{
					"title":   "Tool calling design memo",
					"url":     "mock://search/tool-calling-runtime",
					"snippet": "A schema-driven tool registry helps LLMs call backend capabilities safely and observably.",
				},
				{
					"title":   "Workflow observability checklist",
					"url":     "mock://search/workflow-observability",
					"snippet": "Trace events should capture tool name, arguments, result, duration, status, and errors.",
				},
				{
					"title":   "Day 2 MVP scope",
					"url":     "mock://search/day-2-scope",
					"snippet": "Keep tools deterministic during development so UI and trace behavior remain easy to verify.",
				},
				{
					"title":   "OpenAI-compatible model note",
					"url":     "mock://search/openai-compatible",
					"snippet": "Native tool calling varies by model; JSON action fallback improves compatibility with free models.",
				},
			}
			return map[string]any{
				"query":   input.Query,
				"results": results[:input.Limit],
			}, nil
		},
	}
}
