package toolartifact

import (
	"context"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

const (
	ReadToolName        = "artifact_read"
	SearchToolName      = "artifact_search"
	MaxRuntimeReadBytes = 16 * 1024
)

func (s *Service) ToolBindings() []tools.Binding {
	if s == nil {
		return nil
	}
	return []tools.Binding{s.readBinding(), s.searchBinding()}
}

func (s *Service) ToolNames() []string {
	if s == nil {
		return nil
	}
	return []string{ReadToolName, SearchToolName}
}

func (s *Service) readBinding() tools.Binding {
	return tools.Binding{
		Descriptor: tools.Descriptor{
			Name:        ReadToolName,
			Description: "Read a bounded byte range from an immutable tool-result artifact. Continue with next_offset until complete; never guess storage paths.",
			Concurrency: tools.ConcurrencyPolicy{Mode: tools.ConcurrencyReadOnly},
			Parameters: tools.ObjectSchema(map[string]any{
				"artifact_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"offset":      map[string]any{"type": "integer", "minimum": 0},
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": MaxRuntimeReadBytes},
			}, []string{"artifact_id"}),
		},
		Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var input struct {
				ArtifactID string `json:"artifact_id"`
				Offset     int    `json:"offset"`
				Limit      int    `json:"limit"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			if input.Limit == 0 {
				input.Limit = 8 * 1024
			}
			scope, err := requiredRunScope(ctx)
			if err != nil {
				return nil, err
			}
			result, err := s.store.ReadToolArtifact(scope.RunID, input.ArtifactID, input.Offset, input.Limit)
			if err != nil {
				s.recordFailure(ctx, input.ArtifactID, "read", err)
				return nil, err
			}
			s.record(ctx, domain.EventArtifactRead, eventpkg.ToolArtifactPayload{
				ArtifactID: input.ArtifactID, Operation: "read", Offset: input.Offset,
				Limit: input.Limit, ReturnedBytes: len([]byte(result.Content)),
			})
			return result, nil
		},
	}
}

func (s *Service) searchBinding() tools.Binding {
	return tools.Binding{
		Descriptor: tools.Descriptor{
			Name:        SearchToolName,
			Description: "Search text within one immutable tool-result artifact and return bounded previews with byte offsets.",
			Concurrency: tools.ConcurrencyPolicy{Mode: tools.ConcurrencyReadOnly},
			Parameters: tools.ObjectSchema(map[string]any{
				"artifact_id": map[string]any{"type": "string", "minLength": 1, "maxLength": 128},
				"query":       map[string]any{"type": "string", "minLength": 1, "maxLength": store.MaxToolArtifactSearchQuery},
				"max_matches": map[string]any{"type": "integer", "minimum": 1, "maximum": store.MaxToolArtifactMatches},
			}, []string{"artifact_id", "query"}),
		},
		Handler: func(ctx context.Context, arguments json.RawMessage) (any, error) {
			var input struct {
				ArtifactID string `json:"artifact_id"`
				Query      string `json:"query"`
				MaxMatches int    `json:"max_matches"`
			}
			if err := json.Unmarshal(arguments, &input); err != nil {
				return nil, err
			}
			scope, err := requiredRunScope(ctx)
			if err != nil {
				return nil, err
			}
			result, err := s.store.SearchToolArtifact(scope.RunID, input.ArtifactID, input.Query, input.MaxMatches)
			if err != nil {
				s.recordFailure(ctx, input.ArtifactID, "search", err)
				return nil, err
			}
			s.record(ctx, domain.EventArtifactRead, eventpkg.ToolArtifactPayload{
				ArtifactID: input.ArtifactID, Operation: "search", MatchCount: len(result.Matches),
				ReturnedBytes: previewsBytes(result),
			})
			return result, nil
		},
	}
}

func requiredRunScope(ctx context.Context) (eventpkg.Scope, error) {
	scope := eventpkg.ScopeFromContext(ctx)
	if scope.RunID == "" {
		return eventpkg.Scope{}, errors.New("artifact tool requires run scope")
	}
	return scope, nil
}

func (s *Service) recordFailure(ctx context.Context, artifactID string, operation string, err error) {
	if errors.Is(err, store.ErrToolArtifactExpired) {
		s.record(ctx, domain.EventArtifactExpired, eventpkg.ToolArtifactPayload{ArtifactID: artifactID, Operation: operation})
	}
}

func (s *Service) record(ctx context.Context, eventType domain.RunEventType, payload eventpkg.ToolArtifactPayload) {
	if s.tracer != nil {
		s.tracer.ToolArtifact(context.WithoutCancel(ctx), eventType, payload)
	}
}

func previewsBytes(result domain.ToolArtifactSearchResult) int {
	total := 0
	for _, match := range result.Matches {
		total += len([]byte(match.Preview))
	}
	return total
}
