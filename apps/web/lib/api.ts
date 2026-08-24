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
  executor: ChatExecutor;
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
export type ChatExecutor = "native" | "langchaingo";

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

export type RunReplay = {
  run: RunInfo;
  conversation: Conversation;
  messages: Message[];
  steps: CollaborationStepInfo[];
  summary: RunTraceSummary;
  usage_ledger: RunUsageLedger;
  run_events: RunEvent[];
  stage_checkpoints: Array<Record<string, unknown>>;
  tool_effects: Array<Record<string, unknown>>;
  verification_evidence: Array<Record<string, unknown>>;
  verification_artifacts: Array<Record<string, unknown>>;
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
    conversation: expectConversation(replay.conversation),
    summary: expectRunTraceSummary(replay.summary),
    messages: Array.isArray(replay.messages) ? replay.messages : [],
    steps: Array.isArray(replay.steps) ? replay.steps : [],
    usage_ledger: normalizeRunUsageLedger(replay.usage_ledger, run?.id ?? ""),
    run_events: Array.isArray(replay.run_events) ? replay.run_events : [],
    stage_checkpoints: Array.isArray(replay.stage_checkpoints) ? replay.stage_checkpoints : [],
    tool_effects: Array.isArray(replay.tool_effects) ? replay.tool_effects : [],
    verification_evidence: Array.isArray(replay.verification_evidence) ? replay.verification_evidence : [],
    verification_artifacts: Array.isArray(replay.verification_artifacts) ? replay.verification_artifacts : []
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

export async function streamChat(
  input: {
    conversation_id?: string;
    agent_id?: string;
    message: string;
    mode?: ChatMode;
    executor?: ChatExecutor;
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
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled" | "executor">>
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
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled" | "executor">>
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
    retrieval_enabled: agent.retrieval_enabled ?? true,
    executor: agent.executor ?? "native"
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
