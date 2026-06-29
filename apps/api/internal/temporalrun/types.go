package temporalrun

const (
	RuntimeTemporal = "temporal"

	WorkflowStatusRunning         = "running"
	WorkflowStatusCompleted       = "completed"
	WorkflowStatusFailed          = "failed"
	WorkflowStatusCanceled        = "canceled"
	WorkflowStatusCancelRequested = "cancel_requested"
)

type AgentRunWorkflowInput struct {
	RunID          string
	ConversationID string
	AgentID        string
	UserMessageID  string
	Task           string
	ResumeCanceled bool
	ResumeNote     string
}

type AgentRunWorkflowResult struct {
	RunID  string
	Status string
}
