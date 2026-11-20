package domain

// RunReplay is the detailed aggregate assembled from persisted Run records.
type RunReplay struct {
	Plan                  *RunPlan               `json:"plan,omitempty"`
	Run                   Run                    `json:"run"`
	Projection            RunProjectionSnapshot  `json:"projection"`
	RuntimeSnapshot       *RuntimeSnapshot       `json:"runtime_snapshot,omitempty"`
	Conversation          Conversation           `json:"conversation"`
	Messages              []Message              `json:"messages"`
	Candidate             *RunReplayCandidate    `json:"candidate,omitempty"`
	Steps                 []CollaborationStep    `json:"steps"`
	Summary               RunTraceSummary        `json:"summary"`
	UsageLedger           RunUsageLedger         `json:"usage_ledger"`
	RunEvents             []RunEvent             `json:"run_events"`
	StageCheckpoints      []StageCheckpoint      `json:"stage_checkpoints"`
	ToolEffects           []ToolEffectSummary    `json:"tool_effects"`
	ToolArtifacts         []ToolArtifact         `json:"tool_artifacts"`
	VerificationEvidence  []VerificationEvidence `json:"verification_evidence"`
	VerificationArtifacts []VerificationArtifact `json:"verification_artifacts"`
	TaskStateRevisions    []TaskStateRevision    `json:"task_state_revisions"`
	RecoverySummary       *RecoverySummary       `json:"recovery_summary,omitempty"`
	StructuredResult      *StructuredRunResult   `json:"structured_result,omitempty"`
}

// RunReplayCandidate refers to an existing Message verified by a Run-local receipt.
type RunReplayCandidate struct {
	Question  string `json:"question"`
	MessageID string `json:"message_id"`
}
