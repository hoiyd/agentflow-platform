package modelrequest

import "context"

// Observation contains the exact canonical bytes passed to the model
// transport. Recorder implementations must not receive request headers or
// provider credentials.
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
