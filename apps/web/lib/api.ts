import type { CompletionContractInput } from "./verification";
import { apiArray, apiJSON, apiObject, apiRequest, apiVoid, expectObject, isObject } from "./api-client.ts";

export type Conversation = {
  id: string;
  workspace_id?: string;
  title: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  heartbeat_at?: string;
  completed_at?: string;
};

export type Message = {
  id: string;
  workspace_id?: string;
  conversation_id: string;
  role: "user" | "assistant" | "system";
  content: string;
  citations?: RAGCitation[];
  created_at: string;
};

export type RAGCitation = {
  source_id: string;
  document_id: string;
  document_title: string;
  document_version?: string;
  chunk_id: string;
  source_chunk_ids?: string[];
  section_path?: string[];
  start_offset?: number;
  end_offset?: number;
};

export type RunEvent = {
  id: string;
  type: string;
  schema_version: number;
  sequence: number;
  conversation_id?: string;
  run_id: string;
  stage_id?: string;
  turn_id?: string;
  parent_event_id?: string;
  payload: Record<string, unknown>;
  timestamp: string;
};

export type ChatEvent =
  | { type: "conversation"; conversation_id: string }
  | { type: "run_state"; conversation_id: string; run_id: string; agent_id: string; status: string }
  | { type: "run_progress"; conversation_id: string; run_id: string; agent_id?: string; iteration?: number; max_iterations?: number; elapsed_seconds?: number; max_runtime_seconds?: number; output_chars?: number; max_output_chars?: number; tool_calls?: number; max_tool_calls?: number; stop_reason?: string }
  | { type: "model_delta"; delta: string }
  | { type: "stage_state"; conversation_id: string; run_id: string; agent_id?: string; role: string; status: string; iteration?: number; input?: string; output?: string; error?: string }
  | {
      type: "done";
      conversation_id: string;
      title?: string;
      message_id?: string;
      run_id?: string;
      agent_id?: string;
      status?:
        | "idle"
        | "queued"
        | "running"
        | "waiting_for_user"
        | "completed"
        | "failed"
        | "failed_recoverable"
        | "canceling"
        | "canceled"
        | string;
      verification_status?:
        | "not_required"
        | "pending"
        | "running"
        | "passed"
        | "failed"
        | "blocked"
        | "stale"
        | string;
      citations?: RAGCitation[];
      invalid_citation_ids?: string[];
    }
  | { type: "error"; error: string; code?: string; source?: string; category?: string; retryable?: boolean; request_id?: string };

export type AgentInfo = {
  id: string;
  name: string;
  description: string;
  system_prompt: string;
  tools: string[];
  memory_enabled: boolean;
  retrieval_enabled: boolean;
  archived?: boolean;
  created_at: string;
  updated_at: string;
};

export type ToolInfo = {
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  enabled: boolean;
};

export type ChatMode = "single" | "multi_agent" | "autonomous";

export type RunInfo = {
  workspace_id?: string;
  id: string;
  agent_id: string;
  conversation_id: string;
  status:
    | "idle"
    | "queued"
    | "running"
    | "waiting_for_user"
    | "completed"
    | "failed"
    | "failed_recoverable"
    | "canceling"
    | "canceled"
    | string;
  verification_status?: "not_required" | "pending" | "running" | "passed" | "failed" | "blocked" | "stale" | string;
  completion_contract?: CompletionContractInput | Record<string, unknown>;
  error?: string;
  started_at?: string;
  execution_started_at?: string;
  active_runtime_ms?: number;
  heartbeat_at?: string;
  completed_at?: string;
  created_at: string;
  updated_at: string;
};

export type CollaborationStepInfo = {
  id: string;
  run_id: string;
  conversation_id: string;
  role: string;
  agent_id?: string;
  status:
    | "idle"
    | "queued"
    | "running"
    | "waiting_for_user"
    | "completed"
    | "failed"
    | "failed_recoverable"
    | "canceling"
    | "canceled"
    | string;
  iteration?: number;
  input: string;
  output: string;
  error?: string;
  created_at: string;
  updated_at: string;
};

export type RunTraceSummary = {
  run_id: string;
  status: string;
  total_duration_ms: number;
  total_tokens: number;
  prompt_tokens: number;
  completion_tokens: number;
  token_usage_estimated: boolean;
  llm_calls: number;
  tool_calls: number;
  error_count: number;
};

export type RuntimeRunBudget = {
  max_model_calls?: number;
  max_prompt_tokens?: number;
  max_completion_tokens?: number;
  max_total_tokens?: number;
  max_tool_calls?: number;
  max_runtime_ms?: number;
  max_estimated_cost_micros?: number;
  input_cost_per_million_tokens_micros?: number;
  output_cost_per_million_tokens_micros?: number;
};

export type RunUsageTotals = {
  model_calls: number;
  tool_calls: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  estimated_cost_micros: number;
  open_reservations: number;
};

export type RunUsageEntry = {
  id: string;
  run_id: string;
  operation_id: string;
  stage_id?: string;
  turn_id?: string;
  kind: "model.reservation" | "model.settlement" | "tool.execution" | string;
  purpose: "primary" | "router" | "compaction" | string;
  model?: string;
  tool_name?: string;
  model_calls?: number;
  tool_calls?: number;
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  estimated_cost_micros?: number;
  estimated?: boolean;
  timestamp: string;
};

export type RunUsageLedger = {
  run_id: string;
  budget: RuntimeRunBudget;
  totals: RunUsageTotals;
  entries: RunUsageEntry[];
  updated_at?: string;
};

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

export type RunDelegation = {
  id: string;
  workspace_id: string;
  conversation_id: string;
  parent_run_id: string;
  parent_turn_id: string;
  parent_stage_id?: string;
  child_run_id: string;
  agent_id: string;
  depth: number;
  status: "created" | "running" | "blocked" | "completed" | "failed" | "canceled" | string;
  block_reason?: "child_recovery_required" | string;
  task: string;
  summary?: string;
  output_ref?: string;
  output_hash?: string;
  output_bytes?: number;
  summary_truncated?: boolean;
  timeout_ms: number;
  error?: string;
  created_at: string;
  updated_at: string;
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

export type ToolArtifactRead = {
	artifact: ToolArtifact;
	offset: number;
	content: string;
	next_offset: number;
	complete: boolean;
};

export type ToolArtifactSearchResult = {
	artifact: ToolArtifact;
	query: string;
	matches: Array<{ offset: number; preview: string }>;
	scanned_bytes: number;
	truncated: boolean;
};

export type RunReplay = {
  run: RunInfo;
  projection: RunProjectionSnapshot;
  conversation: Conversation;
  messages: Message[];
  steps: CollaborationStepInfo[];
  summary: RunTraceSummary;
  usage_ledger: RunUsageLedger;
  run_events: RunEvent[];
  stage_checkpoints: Array<Record<string, unknown>>;
	tool_effects: Array<Record<string, unknown>>;
	tool_artifacts: ToolArtifact[];
  verification_evidence: Array<Record<string, unknown>>;
  verification_artifacts: Array<Record<string, unknown>>;
  task_state_revisions: TaskStateRevision[];
  parent_delegation?: RunDelegation;
  child_delegations: RunDelegation[];
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

function normalizeRunReplay(data: unknown): RunReplay {
  const replay = expectObject<Record<string, unknown>>(data, "run replay");
  const run = expectRunInfo(replay.run);
  return {
    run,
    projection: normalizeRunProjection(replay.projection, run, replay.summary, replay.usage_ledger),
    conversation: expectConversation(replay.conversation),
    summary: expectRunTraceSummary(replay.summary),
    messages: Array.isArray(replay.messages) ? replay.messages : [],
    steps: Array.isArray(replay.steps) ? replay.steps : [],
    usage_ledger: normalizeRunUsageLedger(replay.usage_ledger, run?.id ?? ""),
    run_events: Array.isArray(replay.run_events) ? replay.run_events : [],
    stage_checkpoints: Array.isArray(replay.stage_checkpoints) ? replay.stage_checkpoints : [],
		tool_effects: Array.isArray(replay.tool_effects) ? replay.tool_effects : [],
		tool_artifacts: Array.isArray(replay.tool_artifacts) ? replay.tool_artifacts as ToolArtifact[] : [],
    verification_evidence: Array.isArray(replay.verification_evidence) ? replay.verification_evidence : [],
    verification_artifacts: Array.isArray(replay.verification_artifacts) ? replay.verification_artifacts : [],
    task_state_revisions: Array.isArray(replay.task_state_revisions) ? replay.task_state_revisions : [],
    parent_delegation: isObject(replay.parent_delegation) ? replay.parent_delegation as RunDelegation : undefined,
    child_delegations: Array.isArray(replay.child_delegations) ? replay.child_delegations as RunDelegation[] : []
  };
}

function normalizeRunProjection(value: unknown, run: RunInfo, summaryValue: unknown, ledgerValue: unknown): RunProjectionSnapshot {
  const projection = isObject(value) ? value : {};
  const runProjection = isObject(projection.run) ? projection.run : {};
  const usage = isObject(projection.usage) ? projection.usage : {};
  const verification = isObject(projection.verification) ? projection.verification : {};
  const watermark = numberValue(projection.as_of_sequence) ?? 0;
  const strings = (input: unknown): string[] => Array.isArray(input) ? input.filter((item): item is string => typeof item === "string") : [];
  return {
    run: {
      run_id: stringValue(runProjection.run_id) ?? run.id,
      conversation_id: stringValue(runProjection.conversation_id) ?? run.conversation_id,
      status: stringValue(runProjection.status) ?? run.status,
      verification_status: stringValue(runProjection.verification_status) ?? run.verification_status ?? "not_required",
      active_stage_ids: strings(runProjection.active_stage_ids),
      active_turn_ids: strings(runProjection.active_turn_ids),
      active_model_call_ids: strings(runProjection.active_model_call_ids),
      active_tool_call_ids: strings(runProjection.active_tool_call_ids),
      summary: expectRunTraceSummary(runProjection.summary ?? summaryValue),
      as_of_sequence: numberValue(runProjection.as_of_sequence) ?? watermark
    },
    usage: {
      ledger: normalizeRunUsageLedger(usage.ledger ?? ledgerValue, run.id),
      as_of_sequence: numberValue(usage.as_of_sequence) ?? watermark
    },
    verification: {
      status: stringValue(verification.status) ?? run.verification_status ?? "not_required",
      latest_attempt: numberValue(verification.latest_attempt) ?? 0,
      current_subject_hash: stringValue(verification.current_subject_hash),
      evidence_count: numberValue(verification.evidence_count) ?? 0,
      fresh_evidence_count: numberValue(verification.fresh_evidence_count) ?? 0,
      as_of_sequence: numberValue(verification.as_of_sequence) ?? watermark
    },
    as_of_sequence: watermark,
    invariant_failures: Array.isArray(projection.invariant_failures)
      ? projection.invariant_failures.filter((item): item is RuntimeInvariantFailure => isObject(item) && typeof item.code === "string")
      : []
  };
}

const EMPTY_RUN_USAGE_TOTALS: RunUsageTotals = {
  model_calls: 0,
  tool_calls: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  total_tokens: 0,
  estimated_cost_micros: 0,
  open_reservations: 0
};

function normalizeRunUsageLedger(value: unknown, runId: string): RunUsageLedger {
  const ledger = isObject(value) ? value : {};
  return {
    run_id: typeof ledger.run_id === "string" ? ledger.run_id : runId,
    budget: isObject(ledger.budget) ? ledger.budget : {},
    totals: {
      ...EMPTY_RUN_USAGE_TOTALS,
      ...(isObject(ledger.totals) ? ledger.totals : {})
    },
    entries: Array.isArray(ledger.entries) ? ledger.entries : [],
    ...(typeof ledger.updated_at === "string" ? { updated_at: ledger.updated_at } : {})
  };
}

function expectRunInfo(value: unknown): RunInfo {
  const run = expectObject<Record<string, unknown>>(value, "run replay run");
  requireStringFields(run, "run replay run", ["id", "agent_id", "conversation_id", "status"]);
  return run as unknown as RunInfo;
}

function expectConversation(value: unknown): Conversation {
  const conversation = expectObject<Record<string, unknown>>(value, "run replay conversation");
  requireStringFields(conversation, "run replay conversation", ["id", "title"]);
  return conversation as unknown as Conversation;
}

function expectRunTraceSummary(value: unknown): RunTraceSummary {
  const summary = expectObject<Record<string, unknown>>(value, "run replay summary");
  requireStringFields(summary, "run replay summary", ["run_id"]);
  return summary as unknown as RunTraceSummary;
}

function requireStringFields(value: Record<string, unknown>, responseName: string, fields: string[]) {
  for (const field of fields) {
    if (typeof value[field] !== "string" || value[field] === "") {
      throw new Error(`Invalid ${responseName} response: ${field} is required`);
    }
  }
}

export async function listConversations(signal?: AbortSignal): Promise<Conversation[]> {
  return apiArray<Conversation>(
    "/api/conversations",
    { cache: "no-store", signal },
    { errorMessage: "Failed to load conversations" }
  );
}

export async function createConversation(title: string): Promise<Conversation> {
  return apiObject<Conversation>(
    "/api/conversations",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ title })
    },
    { errorMessage: "Failed to create conversation" },
    "conversation"
  );
}

export async function updateConversationTitle(conversationId: string, title: string): Promise<Conversation> {
  return apiObject<Conversation>(
    `/api/conversations/${conversationId}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ title })
    },
    { errorMessage: "Failed to update conversation" },
    "conversation"
  );
}

export async function deleteConversation(conversationId: string): Promise<void> {
  return apiVoid(
    `/api/conversations/${conversationId}`,
    { method: "DELETE" },
    { errorMessage: "Failed to delete conversation" }
  );
}

export async function listMessages(conversationId: string, signal?: AbortSignal): Promise<Message[]> {
  return apiArray<Message>(
    `/api/conversations/${conversationId}/messages`,
    { cache: "no-store", signal },
    { errorMessage: "Failed to load messages" }
  );
}

export async function getTaskState(conversationId: string, signal?: AbortSignal): Promise<TaskState> {
  return apiObject<TaskState>(
    `/api/conversations/${conversationId}/task-state`,
    { cache: "no-store", signal },
    { errorMessage: "Failed to load task state" },
    "task state"
  );
}

export async function patchTaskState(conversationId: string, patch: TaskStatePatch): Promise<TaskStateRevision> {
  return apiObject<TaskStateRevision>(
    `/api/conversations/${conversationId}/task-state`,
    { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(patch) },
    { errorMessage: "Failed to patch task state", includeErrorBody: true },
    "task state revision"
  );
}

export async function listTaskStateRevisions(conversationId: string, signal?: AbortSignal): Promise<TaskStateRevision[]> {
  return apiArray<TaskStateRevision>(
    `/api/conversations/${conversationId}/task-state/revisions`,
    { cache: "no-store", signal },
    { errorMessage: "Failed to load task state revisions" }
  );
}

export async function getTaskStateRevision(conversationId: string, version: number): Promise<TaskStateRevision> {
  return apiObject<TaskStateRevision>(
    `/api/conversations/${conversationId}/task-state/revisions/${version}`,
    { cache: "no-store" },
    { errorMessage: "Failed to load task state revision" },
    "task state revision"
  );
}

export async function streamChat(
  input: {
    conversation_id?: string;
    agent_id?: string;
    message: string;
    mode?: ChatMode;
    completion_contract?: CompletionContractInput;
  },
  onEvent: (event: ChatEvent) => void
) {
  const response = await apiRequest(
    "/api/chat",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Chat request failed", includeErrorBody: true, requireBody: true }
  );

  await readChatEventStream(response, onEvent);
}

export async function continueRun(
  input: { run_id: string; plan: string },
  onEvent: (event: ChatEvent) => void
) {
  const response = await apiRequest(
    `/api/runs/${input.run_id}/continue`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ plan: input.plan })
    },
    { errorMessage: "Continue request failed", requireBody: true }
  );

  await readChatEventStream(response, onEvent);
}

export async function resumeRun(
  input: { run_id: string; user_input: string },
  onEvent: (event: ChatEvent) => void
) {
  const response = await apiRequest(
    `/api/runs/${input.run_id}/resume`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ user_input: input.user_input })
    },
    { errorMessage: "Resume request failed", requireBody: true }
  );

  await readChatEventStream(response, onEvent);
}

export async function cancelRun(runId: string): Promise<RunInfo> {
  return apiObject<RunInfo>(
    `/api/runs/${runId}/cancel`,
    { method: "POST" },
    { errorMessage: "Failed to cancel run" },
    "run"
  );
}

export async function verifyRun(runId: string): Promise<{ run: RunInfo; decision: Record<string, unknown> }> {
  return apiObject<{ run: RunInfo; decision: Record<string, unknown> }>(
    `/api/runs/${runId}/verify`,
    { method: "POST" },
    { errorMessage: "Verify request failed" },
    "verification"
  );
}

async function readChatEventStream(response: Response, onEvent: (event: ChatEvent) => void) {
  if (!response.body) {
    return;
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  while (true) {
    const { value, done } = await reader.read();
    if (done) {
      break;
    }
    buffer += decoder.decode(value, { stream: true });
    const events = buffer.split("\n\n");
    buffer = events.pop() ?? "";

    for (const rawEvent of events) {
      const dataLine = rawEvent
        .split("\n")
        .find((line) => line.startsWith("data: "));
      if (!dataLine) {
        continue;
      }
	  const decoded = JSON.parse(dataLine.slice(6)) as ChatEvent | RunEvent;
	  onEvent(projectRunEvent(decoded));
    }
  }
}

function projectRunEvent(event: ChatEvent | RunEvent): ChatEvent {
  if (!("schema_version" in event)) return event;
  const payload = event.payload;
  if (event.type === "model.delta") return { type: "model_delta", delta: String(payload.delta ?? "") };
  if (event.type === "run.progress") return {
    type: "run_progress", conversation_id: event.conversation_id ?? "", run_id: event.run_id,
    agent_id: stringValue(payload.agent_id), iteration: numberValue(payload.iteration), max_iterations: numberValue(payload.max_iterations),
    elapsed_seconds: numberValue(payload.elapsed_seconds), max_runtime_seconds: numberValue(payload.max_runtime_seconds),
    output_chars: numberValue(payload.output_chars), max_output_chars: numberValue(payload.max_output_chars),
    tool_calls: numberValue(payload.tool_calls), max_tool_calls: numberValue(payload.max_tool_calls), stop_reason: stringValue(payload.stop_reason)
  };
  if (event.type.startsWith("stage.")) return {
    type: "stage_state", conversation_id: event.conversation_id ?? "", run_id: event.run_id,
    agent_id: stringValue(payload.agent_id), role: stringValue(payload.name) ?? "stage",
    status: stringValue(payload.status) ?? event.type.slice("stage.".length), iteration: numberValue(payload.iteration),
    input: stringValue(payload.input), output: stringValue(payload.output), error: stringValue(payload.error)
  };
  if (event.type.startsWith("run.")) return {
    type: "run_state", conversation_id: event.conversation_id ?? "", run_id: event.run_id,
    agent_id: stringValue(payload.agent_id) ?? "", status: stringValue(payload.status) ?? event.type.slice("run.".length)
  };
  return { type: "model_delta", delta: "" };
}

function stringValue(value: unknown): string | undefined { return typeof value === "string" ? value : undefined; }
function numberValue(value: unknown): number | undefined { return typeof value === "number" ? value : undefined; }

export async function listAgents(): Promise<AgentInfo[]> {
  const agents = await apiArray<AgentInfo>(
    "/api/agents",
    { cache: "no-store" },
    { errorMessage: "Failed to load agents" }
  );
  return agents.map(normalizeAgentInfo);
}

export async function createAgent(
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled">>
): Promise<AgentInfo> {
  const agent = await apiObject<AgentInfo>(
    "/api/agents",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Failed to create agent", includeErrorBody: true },
    "agent"
  );
  return normalizeAgentInfo(agent);
}

export async function updateAgent(
  agentId: string,
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled">>
): Promise<AgentInfo> {
  const agent = await apiObject<AgentInfo>(
    `/api/agents/${agentId}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Failed to update agent", includeErrorBody: true },
    "agent"
  );
  return normalizeAgentInfo(agent);
}

export async function archiveAgent(agentId: string): Promise<void> {
  return apiVoid(
    `/api/agents/${agentId}`,
    { method: "DELETE" },
    { errorMessage: "Failed to archive agent", includeErrorBody: true }
  );
}

function normalizeAgentInfo(agent: AgentInfo): AgentInfo {
  return {
    ...agent,
    tools: Array.isArray(agent.tools) ? agent.tools : [],
    memory_enabled: agent.memory_enabled ?? true,
    retrieval_enabled: agent.retrieval_enabled ?? true
  };
}

export async function listTools(): Promise<ToolInfo[]> {
  return apiArray<ToolInfo>("/api/tools", { cache: "no-store" }, { errorMessage: "Failed to load tools" });
}

export async function listRuns(signal?: AbortSignal): Promise<RunInfo[]> {
  return apiArray<RunInfo>(
    "/api/runs",
    { cache: "no-store", signal },
    { errorMessage: "Failed to load runs" }
  );
}

export async function listCollaborationSteps(runId: string, signal?: AbortSignal): Promise<CollaborationStepInfo[]> {
  return apiArray<CollaborationStepInfo>(
    `/api/runs/${runId}/collaboration_steps`,
    { cache: "no-store", signal },
    { errorMessage: "Failed to load collaboration steps" }
  );
}

export async function getRunReplay(runId: string): Promise<RunReplay> {
  const data = await apiJSON(
    `/api/runs/${runId}/replay`,
    { cache: "no-store" },
    { errorMessage: "Failed to load run replay" }
  );
  return normalizeRunReplay(data);
}

export async function listToolArtifacts(runId: string): Promise<ToolArtifact[]> {
	const response = await apiObject<{ artifacts?: ToolArtifact[] }>(
		`/api/runs/${encodeURIComponent(runId)}/artifacts`,
		{ cache: "no-store" },
		{ errorMessage: "Failed to load tool artifacts" },
		"tool artifact list"
	);
	return Array.isArray(response.artifacts) ? response.artifacts : [];
}

export async function readToolArtifact(runId: string, artifactId: string, offset = 0, limit = 8192): Promise<ToolArtifactRead> {
	const query = new URLSearchParams({ offset: String(offset), limit: String(limit) });
	return apiObject<ToolArtifactRead>(
		`/api/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(artifactId)}?${query}`,
		{ cache: "no-store" },
		{ errorMessage: "Failed to read tool artifact", includeErrorBody: true },
		"tool artifact read"
	);
}

export async function searchToolArtifact(runId: string, artifactId: string, queryText: string, maxMatches = 5): Promise<ToolArtifactSearchResult> {
	const query = new URLSearchParams({ q: queryText, max_matches: String(maxMatches) });
	return apiObject<ToolArtifactSearchResult>(
		`/api/runs/${encodeURIComponent(runId)}/artifacts/${encodeURIComponent(artifactId)}/search?${query}`,
		{ cache: "no-store" },
		{ errorMessage: "Failed to search tool artifact", includeErrorBody: true },
		"tool artifact search"
	);
}

export async function getRunProjection(runId: string): Promise<RunProjectionSnapshot> {
  const data = await apiJSON(
    `/api/runs/${runId}/projection`,
    { cache: "no-store" },
    { errorMessage: "Failed to load run projection" }
  );
  const projection = expectObject<Record<string, unknown>>(data, "run projection");
  const runProjection = expectObject<Record<string, unknown>>(projection.run, "run projection state");
  const run = {
    id: stringValue(runProjection.run_id) ?? runId,
    agent_id: "",
    conversation_id: stringValue(runProjection.conversation_id) ?? "",
    status: stringValue(runProjection.status) ?? "",
    verification_status: stringValue(runProjection.verification_status),
    created_at: "",
    updated_at: ""
  } satisfies RunInfo;
  return normalizeRunProjection(projection, run, runProjection.summary, isObject(projection.usage) ? projection.usage.ledger : undefined);
}

export async function getRunUsage(runId: string): Promise<RunUsageLedger> {
  const data = await apiJSON(
    `/api/runs/${runId}/usage`,
    { cache: "no-store" },
    { errorMessage: "Failed to load run usage" }
  );
  return normalizeRunUsageLedger(data, runId);
}

export async function getRunModelRequests(runId: string, includeContent = false): Promise<ModelRequestDebugResponse> {
  const query = includeContent ? "?include_content=true" : "";
  const result = await apiObject<ModelRequestDebugResponse>(
    `/api/runs/${runId}/model_requests${query}`,
    { cache: "no-store" },
    { errorMessage: "Failed to load model requests" },
    "model request debug"
  );
  return { ...result, records: Array.isArray(result.records) ? result.records : [] };
}

export async function getEpisodeReport(runId: string): Promise<EpisodeReport> {
  return apiObject<EpisodeReport>(
    `/api/runs/${runId}/episode`,
    { cache: "no-store" },
    { errorMessage: "Failed to load episode report" },
    "episode report"
  );
}

export async function setToolEnabled(name: string, enabled: boolean): Promise<ToolInfo[]> {
  const action = enabled ? "enable" : "disable";
  return apiArray<ToolInfo>(
    `/api/tools/${encodeURIComponent(name)}/${action}`,
    { method: "POST" },
    { errorMessage: "Failed to update tool" }
  );
}
