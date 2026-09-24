package openai

import (
	"context"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
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

func (c *Client) recordModelRequest(ctx context.Context, modelCallID, operation, model string, payload []byte) (requestcontrol.AttemptRef, error) {
	if c == nil || c.requestRecorder == nil {
		return requestcontrol.AttemptRef{}, nil
	}
	provider := providerForURL(c.baseURL)
	if model == "local_fallback" {
		provider = "local"
	}
	manifest := requestManifestFromContext(ctx)
	observation := requestcontrol.Observation{
		ModelCallID: strings.TrimSpace(modelCallID), Operation: strings.TrimSpace(operation),
		Provider: provider, Model: strings.TrimSpace(model), ContextManifestID: manifest.id,
		SourceTokenBreakdown: manifest.sourceTokenBreakdown,
		Payload:              append([]byte(nil), payload...),
	}
	if recorder, ok := c.requestRecorder.(requestcontrol.AttemptRecorder); ok {
		return recorder.Begin(ctx, observation)
	}
	return requestcontrol.AttemptRef{}, c.requestRecorder.Record(ctx, observation)
}

func (c *Client) finishModelAttempt(ctx context.Context, ref requestcontrol.AttemptRef, started, firstToken time.Time, usage Usage, finishReason string, attemptErr error) {
	if ref.RecordID == "" {
		return
	}
	recorder, ok := c.requestRecorder.(requestcontrol.AttemptRecorder)
	if !ok {
		return
	}
	finished := time.Now()
	outcome := requestcontrol.AttemptOutcome{
		Status: "completed", DurationMS: finished.Sub(started).Milliseconds(),
		PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens,
		UsageEstimated: usage.Estimated, UsageAvailable: usage.Valid(),
		FinishReason: finishReason,
	}
	if timing := requestcontrol.AttemptTimingFromContext(ctx); timing != nil {
		if timing.Limited {
			rateWaitMS, permitWaitMS := timing.RateWait.Milliseconds(), timing.PermitWait.Milliseconds()
			outcome.RateLimitWaitMS, outcome.ModelPermitWaitMS = &rateWaitMS, &permitWaitMS
		}
		if !timing.TransportStartedAt.IsZero() {
			httpDurationMS := finished.Sub(timing.TransportStartedAt).Milliseconds()
			outcome.HTTPDurationMS = &httpDurationMS
			if !firstToken.IsZero() {
				httpFirstTokenMS := firstToken.Sub(timing.TransportStartedAt).Milliseconds()
				outcome.HTTPTimeToFirstTokenMS = &httpFirstTokenMS
			}
		}
	}
	if !firstToken.IsZero() {
		firstTokenMS := firstToken.Sub(started).Milliseconds()
		outcome.TimeToFirstTokenMS = &firstTokenMS
		if !usage.Estimated && usage.CompletionTokens > 0 {
			generationMS := outcome.DurationMS - firstTokenMS
			if generationMS > 0 {
				outcome.OutputTokensPerSecond = float64(usage.CompletionTokens) * 1000 / float64(generationMS)
			}
		}
	}
	if attemptErr != nil {
		modelErr := classifyModelError("", attemptErr)
		outcome.Status, outcome.ErrorKind, outcome.HTTPStatus = "failed", string(modelErr.Kind), modelErr.StatusCode
	}
	if err := recorder.Finish(context.WithoutCancel(ctx), ref, outcome); err != nil {
		log.Printf("model_attempt_telemetry_error record_id=%s error=%q", ref.RecordID, err.Error())
	}
}
