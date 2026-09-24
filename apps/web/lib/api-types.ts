import type { components } from "./api-contract.gen";

export type ContractSchemas = components["schemas"];

export type APIHealth = ContractSchemas["HealthResponse"];
export type ChatRequest = ContractSchemas["ChatRequest"];
export type AgentConfigInput = ContractSchemas["AgentConfigRequest"];
export type Conversation = ContractSchemas["Conversation"];
type APIMessage = ContractSchemas["Message"];
export type Message = Omit<APIMessage, "workspace_id"> & { workspace_id?: string };
export type RunEvent = ContractSchemas["RunEvent"];
export type ChatEvent = Exclude<ContractSchemas["ChatStreamEvent"], RunEvent>;
export type AgentInfo = ContractSchemas["Agent"];
export type AgentRoutingRequirements = ContractSchemas["AgentRoutingRequirements"];
export type ToolInfo = ContractSchemas["ToolInfo"];
export type ChatMode = ContractSchemas["ChatMode"];
export type RunInfo = ContractSchemas["Run"];
export type OperatorAttentionItem = ContractSchemas["OperatorAttentionItem"];
export type CollaborationStepInfo = ContractSchemas["CollaborationStep"];
export type RunTraceSummary = ContractSchemas["RunTraceSummary"];
export type RunUsageTotals = ContractSchemas["RunUsageTotals"];
export type RunUsageEntry = ContractSchemas["RunUsageEntry"];
export type RunUsageLedger = ContractSchemas["RunUsageLedger"];

export type RuntimeInvariantFailure = {
  code: string;
  owner: string;
  run_id: string;
  event_id?: string;
  sequence?: number;
  message: string;
};

export type RunProjectionSnapshot = {
  run: {
    run_id: string;
    conversation_id: string;
    status: string;
    verification_status: string;
    active_stage_ids: string[];
    active_turn_ids: string[];
    active_model_call_ids: string[];
    active_tool_call_ids: string[];
    summary: RunTraceSummary;
    as_of_sequence: number;
  };
  usage: { ledger: RunUsageLedger; as_of_sequence: number };
  verification: {
    status: string;
    latest_attempt: number;
    current_subject_hash?: string;
    evidence_count: number;
    fresh_evidence_count: number;
    as_of_sequence: number;
  };
  as_of_sequence: number;
  invariant_failures: RuntimeInvariantFailure[];
};

export type ModelRequestEnvelope = {
  id: string;
  run_id: string;
  conversation_id: string;
  stage_id?: string;
  turn_id?: string;
  model_call_id: string;
  attempt: number;
  operation: string;
  provider: string;
  model: string;
  context_manifest_id?: string;
  runtime_snapshot_hash: string;
  payload_hash: string;
  payload_bytes: number;
  parameters: Record<string, unknown>;
  source_token_breakdown: Record<string, number>;
  message_count: number;
  tool_count: number;
  created_at: string;
};

export type ModelRequestCapture = {
  mode: "metadata_only" | "redacted" | "full";
  content?: string;
  content_hash?: string;
  original_bytes: number;
  stored_bytes: number;
  redacted: boolean;
  redaction_strategy?: string;
  redaction_count: number;
  truncated: boolean;
  reconstructable: boolean;
  expires_at?: string;
  expired: boolean;
};

export type ModelRequestDebugResponse = {
  run_id: string;
  reconstructability_status: "valid" | "invalid";
  invariant_error?: string;
  records: Array<{
    envelope: ModelRequestEnvelope;
    capture: ModelRequestCapture;
    manifest?: Record<string, unknown>;
    source_diff: {
      envelope_selected_tokens: Record<string, number>;
      manifest_selected_tokens: Record<string, number>;
      manifest_excluded_tokens: Record<string, number>;
      matches_envelope: boolean;
    };
  }>;
};

export type TaskItemStatus = "pending" | "in_progress" | "completed" | "canceled";
export type TaskBlockerStatus = "open" | "resolved";

export type TaskState = {
  schema_version: number;
  workspace_id: string;
  conversation_id: string;
  version: number;
  goal?: string;
  tasks: Array<{ id: string; title: string; details?: string; status: TaskItemStatus; artifact_refs?: string[] }>;
  decisions: Array<{ id: string; statement: string; rationale?: string; supersedes_id?: string }>;
  constraints: Array<{ id: string; statement: string }>;
  blockers: Array<{ id: string; description: string; status: TaskBlockerStatus }>;
  artifact_refs: string[];
  updated_at: string;
};

export type TaskStateOperation =
  | { type: "set_goal"; goal: string }
  | { type: "clear_goal" }
  | { type: "upsert_task"; task: TaskState["tasks"][number] }
  | { type: "set_task_status"; task_id: string; task_status: TaskItemStatus }
  | { type: "remove_task"; task_id: string }
  | { type: "add_decision"; decision: TaskState["decisions"][number] }
  | { type: "upsert_constraint"; constraint: TaskState["constraints"][number] }
  | { type: "remove_constraint"; constraint_id: string }
  | { type: "upsert_blocker"; blocker: TaskState["blockers"][number] }
  | { type: "resolve_blocker"; blocker_id: string }
  | { type: "remove_blocker"; blocker_id: string }
  | { type: "add_artifact_ref"; artifact_ref: string }
  | { type: "remove_artifact_ref"; artifact_ref: string };

export type TaskStatePatch = {
  expected_version: number;
  operations: TaskStateOperation[];
};

export type TaskStateRevision = {
  id: string;
  workspace_id: string;
  conversation_id: string;
  version: number;
  previous_version: number;
  patch: TaskStatePatch;
  state: TaskState;
  source: {
    actor_type: string;
    actor_id?: string;
    run_id?: string;
    stage_id?: string;
    turn_id?: string;
    source_message_id?: string;
  };
  created_at: string;
};

export type ToolArtifact = {
	id: string;
	schema_version: number;
	run_id: string;
	stage_id?: string;
	turn_id?: string;
	tool_call_id: string;
	tool_name: string;
	definition_revision?: string;
	media_type: string;
	content_hash: string;
	original_byte_size: number;
	stored_byte_size: number;
	redacted: boolean;
	redaction_strategy?: string;
	redaction_count: number;
	created_at: string;
	expires_at?: string;
	expired?: boolean;
};

export type ToolEffectReconciliationAction =
	| "confirm_committed"
	| "confirm_failed"
	| "retry_with_same_key"
	| "compensate";

export type ToolEffect = {
	idempotency_key: string;
	version: number;
	run_id: string;
	stage_id: string;
	turn_id?: string;
	tool_call_id: string;
	tool_name: string;
	definition_revision?: string;
	request_hash: string;
	status: string;
	has_result: boolean;
	error?: string;
	created_at: string;
	updated_at: string;
	available_actions?: ToolEffectReconciliationAction[];
};

export type RecoveryEvidence = {
	kind: string;
	id?: string;
	status?: string;
	summary: string;
	artifact_refs?: string[];
};

export type RecoveryAction = {
	kind: string;
	label: string;
	enabled: boolean;
	target_id?: string;
	unavailable_reason?: string;
};

export type RecoverySummary = {
	reason: string;
	title: string;
	message: string;
	evidence: RecoveryEvidence[];
	artifact_refs: string[];
	actions: RecoveryAction[];
};

export type RunReplay = {
  run: RunInfo;
  projection: RunProjectionSnapshot;
  runtime_snapshot?: Record<string, unknown>;
  conversation: Conversation;
  messages: Message[];
  steps: CollaborationStepInfo[];
  summary: RunTraceSummary;
  usage_ledger: RunUsageLedger;
  run_events: RunEvent[];
  stage_checkpoints: Array<Record<string, unknown>>;
	tool_effects: ToolEffect[];
	tool_artifacts: ToolArtifact[];
  verification_evidence: Array<Record<string, unknown>>;
  verification_artifacts: Array<Record<string, unknown>>;
  task_state_revisions: TaskStateRevision[];
	recovery_summary?: RecoverySummary;
};

export type EpisodeReport = {
  run: RunInfo;
  conversation: Conversation;
  agent: AgentInfo;
  task: string;
  final_output: string;
  messages: Message[];
  steps: CollaborationStepInfo[];
  trace_summary: RunTraceSummary;
  retrievals: {
    event_count: number;
    memories: Record<string, unknown>[];
    chunks: Record<string, unknown>[];
  };
  llm_calls: Array<{
    event_id: string;
    step_id?: string;
    role?: string;
    agent_id?: string;
    model?: string;
    framework?: string;
    prompt_tokens?: number;
    completion_tokens?: number;
    total_tokens?: number;
    token_usage_estimated?: boolean;
    output_chars?: number;
    duration_ms?: number;
  }>;
  tool_calls: Array<{
    event_id: string;
    step_id?: string;
    tool_name?: string;
    tool_call_id?: string;
    error?: string;
    duration_ms?: number;
  }>;
  errors: Array<{
    source: string;
    event_id?: string;
    step_id?: string;
    message: string;
  }>;
  verification: {
    status: "not_required" | "pending" | "running" | "passed" | "failed" | "blocked" | "stale" | string;
    subject_hash?: string;
    contract?: Record<string, unknown>;
    evidence: string[];
    warnings: string[];
    records: Array<Record<string, unknown>>;
    artifacts: Array<Record<string, unknown>>;
  };
};
