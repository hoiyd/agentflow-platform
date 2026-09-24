package requestcontrol

import "context"

// Observation contains canonical model-request bytes from a physical transport
// attempt or a deterministic offline reconstruction. Recorder implementations
// must not receive request headers or provider credentials.
type Observation struct {
	ModelCallID          string
	Operation            string
	Provider             string
	Model                string
	ContextManifestID    string
	SourceTokenBreakdown map[string]int
	Payload              []byte
}

type Recorder interface {
	Record(context.Context, Observation) error
}

// AttemptRecorder extends request capture with an outcome for the same durable
// physical attempt. Offline capture-only recorders need not implement it.
type AttemptRecorder interface {
	Recorder
	Begin(context.Context, Observation) (AttemptRef, error)
	Finish(context.Context, AttemptRef, AttemptOutcome) error
}

type AttemptRef struct {
	RecordID    string
	ModelCallID string
	Attempt     int
}

type AttemptOutcome struct {
	Status                string
	DurationMS            int64
	TimeToFirstTokenMS    *int64
	OutputTokensPerSecond float64
	PromptTokens          int
	CompletionTokens      int
	TotalTokens           int
	UsageEstimated        bool
	UsageAvailable        bool
	FinishReason          string
	ErrorKind             string
	HTTPStatus            int
}
