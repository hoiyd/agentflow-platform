import type { CompletionContractInput } from "./verification";
import {
  apiArray, apiJSON, apiObject, apiRequest, apiVoid, expectObject, isObject, stringValue
} from "./api-client.ts";
import { readChatEventStream, runStatusValue } from "./run-stream.ts";
import { normalizeRunProjection, normalizeRunReplay, normalizeRunUsageLedger } from "./run-replay-normalization.ts";
import type {
  AgentConfigInput, AgentInfo, AgentRoutingRequirements, APIHealth, ChatEvent, ChatRequest,
  CollaborationStepInfo, ContractSchemas, Conversation, EpisodeReport, Message,
  ModelRequestDebugResponse, OperatorAttentionItem, RunInfo, RunProjectionSnapshot,
  RunReplay, RunUsageLedger, TaskState, TaskStatePatch, TaskStateRevision, ToolEffect,
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
