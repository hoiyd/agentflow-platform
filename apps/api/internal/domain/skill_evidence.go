package domain

// SkillEvidence is derived from frozen bindings, request-backed Manifests and
// Tool receipts. Inclusion proves input, not that the model followed a method.
type SkillEvidence struct {
	Name         string `json:"name"`
	Hash         string `json:"hash"`
	AgentID      string `json:"agent_id"`
	StageID      string `json:"stage_id,omitempty"`
	Bound        bool   `json:"bound"`
	Instructions string `json:"instructions"`
	Activation   string `json:"activation"`
	// FirstSequence locates the Manifest; FirstRequestSequence locates the
	// matching physical-attempt envelope event when that event is retained.
	FirstSequence        int64                   `json:"first_sequence,omitempty"`
	FirstRequestSequence int64                   `json:"first_request_sequence,omitempty"`
	ManifestID           string                  `json:"manifest_id,omitempty"`
	EventID              string                  `json:"event_id,omitempty"`
	RequestID            string                  `json:"request_id,omitempty"`
	EstimatedTokens      int                     `json:"estimated_tokens,omitempty"`
	Resources            []SkillResourceEvidence `json:"resources"`
	Failures             []SkillFailureEvidence  `json:"failures"`
}

type SkillResourceEvidence struct {
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	TotalBytes int    `json:"total_bytes"`
	EventID    string `json:"event_id"`
	Sequence   int64  `json:"sequence"`
}

type SkillFailureEvidence struct {
	Tool     string `json:"tool"`
	Code     string `json:"code"`
	Path     string `json:"path,omitempty"`
	EventID  string `json:"event_id"`
	Sequence int64  `json:"sequence"`
}
