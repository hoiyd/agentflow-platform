package openai

import (
	"context"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/modelrequest"
)

type requestManifestKey struct{}

type requestManifest struct {
	id                   string
	sourceTokenBreakdown map[string]int
}

func withRequestManifest(ctx context.Context, manifest domain.ContextManifest) context.Context {
	manifestID := strings.TrimSpace(manifest.ID)
	if manifestID == "" {
		return ctx
	}
	breakdown := map[string]int{}
	for _, entry := range manifest.Entries {
		if entry.Selected && entry.EstimatedTokens > 0 {
			breakdown[entry.Source] += entry.EstimatedTokens
		}
	}
	return context.WithValue(ctx, requestManifestKey{}, requestManifest{id: manifestID, sourceTokenBreakdown: breakdown})
}

func requestManifestFromContext(ctx context.Context) requestManifest {
	value, _ := ctx.Value(requestManifestKey{}).(requestManifest)
	value.id = strings.TrimSpace(value.id)
	return value
}

func (c *Client) recordModelRequest(ctx context.Context, modelCallID, operation, model string, payload []byte) error {
	if c == nil || c.requestRecorder == nil {
		return nil
	}
	provider := providerForURL(c.baseURL)
	if model == "local_fallback" {
		provider = "local"
	}
	manifest := requestManifestFromContext(ctx)
	return c.requestRecorder.Record(ctx, modelrequest.Observation{
		ModelCallID: strings.TrimSpace(modelCallID), Operation: strings.TrimSpace(operation),
		Provider: provider, Model: strings.TrimSpace(model), ContextManifestID: manifest.id,
		SourceTokenBreakdown: manifest.sourceTokenBreakdown,
		Payload:              append([]byte(nil), payload...),
	})
}
