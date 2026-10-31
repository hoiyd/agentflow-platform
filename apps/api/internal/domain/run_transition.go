package domain

// RunTransition compares From with the durable status and commits the new status
// and its lifecycle fact together. A stale writer receives the current Run only.
type RunTransition struct {
	RunID     string
	From      RunStatus
	To        RunStatus
	Error     string
	EventType RunEventType
	Payload   map[string]any
}

type RunTransitionResult struct {
	Run     Run
	Events  []RunEvent
	Applied bool
}
