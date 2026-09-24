import type { CompletionContractInput } from "./verification";
import {
  apiArray, apiJSON, apiObject, apiRequest, apiVoid, expectObject, isObject, numberValue, stringValue
} from "./api-client.ts";
import { readChatEventStream, runStatusValue } from "./run-stream.ts";
import type {
  AgentConfigInput, AgentInfo, AgentRoutingRequirements, APIHealth, ChatEvent, ChatRequest,
  CollaborationStepInfo, ContractSchemas, Conversation, EpisodeReport, Message,
  ModelRequestDebugResponse, OperatorAttentionItem, RecoverySummary, RunInfo, RunProjectionSnapshot,
  RunReplay, RunTraceSummary, RuntimeInvariantFailure, RunUsageLedger, RunUsageTotals,
  TaskState, TaskStatePatch, TaskStateRevision, ToolArtifact, ToolEffect,
  ToolEffectReconciliationAction, ToolInfo
} from "./api-types.ts";

export type {
  AgentConfigInput, AgentInfo, AgentRoutingRequirements, APIHealth, ChatEvent, ChatMode, ChatRequest,
  CollaborationStepInfo, Conversation, EpisodeReport, Message, ModelRequestCapture,
  ModelRequestDebugResponse, ModelRequestEnvelope, OperatorAttentionItem, RecoveryAction,
  RecoveryEvidence, RecoverySummary, RunEvent, RunInfo, RunProjectionSnapshot, RunReplay,
  RunTraceSummary, RunUsageEntry, RunUsageLedger, RunUsageTotals, RuntimeInvariantFailure,
  TaskBlockerStatus, TaskItemStatus, TaskState, TaskStateOperation, TaskStatePatch,
  TaskStateRevision, ToolArtifact, ToolEffect, ToolEffectReconciliationAction, ToolInfo
} from "./api-types.ts";
export { observeRunEvents } from "./run-stream.ts";

function normalizeRunReplay(data: unknown): RunReplay {
  const replay = expectObject<Record<string, unknown>>(data, "run replay");
  const run = expectRunInfo(replay.run);
  return {
    run,
    projection: normalizeRunProjection(replay.projection, run, replay.summary, replay.usage_ledger),
    runtime_snapshot: isObject(replay.runtime_snapshot) ? replay.runtime_snapshot : undefined,
    conversation: expectConversation(replay.conversation),
    summary: expectRunTraceSummary(replay.summary),
    messages: Array.isArray(replay.messages) ? replay.messages : [],
    steps: Array.isArray(replay.steps) ? replay.steps : [],
    usage_ledger: normalizeRunUsageLedger(replay.usage_ledger, run?.id ?? ""),
    run_events: Array.isArray(replay.run_events) ? replay.run_events : [],
    stage_checkpoints: Array.isArray(replay.stage_checkpoints) ? replay.stage_checkpoints : [],
		tool_effects: Array.isArray(replay.tool_effects) ? replay.tool_effects as ToolEffect[] : [],
		tool_artifacts: Array.isArray(replay.tool_artifacts) ? replay.tool_artifacts as ToolArtifact[] : [],
    verification_evidence: Array.isArray(replay.verification_evidence) ? replay.verification_evidence : [],
    verification_artifacts: Array.isArray(replay.verification_artifacts) ? replay.verification_artifacts : [],
    task_state_revisions: Array.isArray(replay.task_state_revisions) ? replay.task_state_revisions : [],
    recovery_summary: normalizeRecoverySummary(replay.recovery_summary)
  };
}

function normalizeRecoverySummary(value: unknown): RecoverySummary | undefined {
  if (!isObject(value)) return undefined;
  const strings = (input: unknown) => Array.isArray(input)
    ? input.filter((item): item is string => typeof item === "string")
    : [];
  return {
    reason: stringValue(value.reason) ?? "",
    title: stringValue(value.title) ?? "",
    message: stringValue(value.message) ?? "",
    evidence: Array.isArray(value.evidence)
      ? value.evidence.filter(isObject).map((item) => ({
          kind: stringValue(item.kind) ?? "unknown",
          summary: stringValue(item.summary) ?? "",
          id: stringValue(item.id),
          status: stringValue(item.status),
          artifact_refs: strings(item.artifact_refs)
        }))
      : [],
    artifact_refs: strings(value.artifact_refs),
    actions: Array.isArray(value.actions)
      ? value.actions.filter(isObject).map((item) => ({
          kind: stringValue(item.kind) ?? "unknown",
          label: stringValue(item.label) ?? "Unknown action",
          enabled: item.enabled === true,
          target_id: stringValue(item.target_id),
          unavailable_reason: stringValue(item.unavailable_reason)
        }))
      : []
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

export async function getAPIHealth(signal?: AbortSignal): Promise<APIHealth> {
  const health = await apiObject<APIHealth>(
    "/health",
    { cache: "no-store", signal },
    { errorMessage: "Failed to reach API" },
    "API health"
  );
  if (health.status !== "ok") {
    throw new Error(`API health check returned ${health.status || "an unknown status"}`);
  }
  return health;
}

export async function createConversation(title: string): Promise<Conversation> {
  const input: ContractSchemas["CreateConversationRequest"] = { title };
  return apiObject<Conversation>(
    "/api/conversations",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Failed to create conversation" },
    "conversation"
  );
}

export async function updateConversationTitle(conversationId: string, title: string): Promise<Conversation> {
  const input: ContractSchemas["UpdateConversationRequest"] = { title };
  return apiObject<Conversation>(
    `/api/conversations/${conversationId}`,
    {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
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

export async function streamChat(
  input: Omit<ChatRequest, "completion_contract"> & { completion_contract?: CompletionContractInput },
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
  input: { run_id: string; plan: string; routing_requirements?: AgentRoutingRequirements },
  onEvent: (event: ChatEvent) => void
) {
  const body: ContractSchemas["ContinueRunRequest"] = {
    plan: input.plan,
    routing_requirements: input.routing_requirements
  };
  const response = await apiRequest(
    `/api/runs/${input.run_id}/continue`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    },
    { errorMessage: "Continue request failed", requireBody: true }
  );

  await readChatEventStream(response, onEvent);
}

export async function resumeRun(
  input: { run_id: string; user_input: string },
  onEvent: (event: ChatEvent) => void
) {
  const body: ContractSchemas["ResumeRunRequest"] = { user_input: input.user_input };
  const response = await apiRequest(
    `/api/runs/${input.run_id}/resume`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body)
    },
    { errorMessage: "Resume request failed", includeErrorBody: true, requireBody: true }
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

export async function listAgents(): Promise<AgentInfo[]> {
  const agents = await apiArray<AgentInfo>(
    "/api/agents",
    { cache: "no-store" },
    { errorMessage: "Failed to load agents" }
  );
  return agents.map(normalizeAgentInfo);
}

export async function createAgent(
  input: AgentConfigInput
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
  input: AgentConfigInput
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
    routing_hints: {
      capabilities: agent.routing_hints?.capabilities ?? [],
      task_examples: agent.routing_hints?.task_examples ?? [],
      exclusions: agent.routing_hints?.exclusions ?? []
    },
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

export async function listRunAttention(signal?: AbortSignal): Promise<OperatorAttentionItem[]> {
  return apiArray<OperatorAttentionItem>(
    "/api/runs/attention",
    { cache: "no-store", signal },
    { errorMessage: "Failed to load operator attention" }
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

export async function listToolEffects(runId: string): Promise<ToolEffect[]> {
	const response = await apiObject<{ effects?: ToolEffect[] }>(
		`/api/runs/${encodeURIComponent(runId)}/tool-effects`,
		{ cache: "no-store" },
		{ errorMessage: "Failed to load tool effects", includeErrorBody: true },
		"tool effect list"
	);
	return Array.isArray(response.effects) ? response.effects : [];
}

export async function reconcileToolEffect(
	runId: string,
	idempotencyKey: string,
	command: {
		command_id: string;
		action: ToolEffectReconciliationAction;
		expected_version: number;
		actor: string;
		reason: string;
		result?: unknown;
	}
): Promise<{ applied: boolean; outcome: string; effect: ToolEffect }> {
	return apiObject<{ applied: boolean; outcome: string; effect: ToolEffect }>(
		`/api/runs/${encodeURIComponent(runId)}/tool-effects/${encodeURIComponent(idempotencyKey)}/reconcile`,
		{ method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(command) },
		{ errorMessage: "Failed to reconcile tool effect", includeErrorBody: true },
		"tool effect reconciliation"
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
    workspace_id: "",
    agent_id: "",
    conversation_id: stringValue(runProjection.conversation_id) ?? "",
    status: runStatusValue(runProjection.status) ?? "running",
    verification_status: "not_required",
    active_runtime_ms: 0,
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
