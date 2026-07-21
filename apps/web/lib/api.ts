import createClient from "openapi-fetch";
import type { components, paths } from "./api/generated";

type Schemas = components["schemas"];

export type Conversation = Schemas["Conversation"];

export type Message = Schemas["Message"];

export type RunEvent = Schemas["RunEvent"];

export type ChatEvent = Exclude<Schemas["ChatStreamEvent"], RunEvent>;

export type AgentInfo = Schemas["Agent"];

export type ToolInfo = Schemas["ToolInfo"];

export type ChatMode = Schemas["ChatMode"];
export type ChatExecutor = Schemas["ChatExecutor"];

export type RunInfo = Schemas["Run"];

export type CollaborationStepInfo = Schemas["CollaborationStep"];

export type RunTraceSummary = Schemas["RunTraceSummary"];

export type RuntimeRunBudget = Schemas["RuntimeRunBudget"];

export type RunUsageTotals = Schemas["RunUsageTotals"];

export type RunUsageEntry = Schemas["RunUsageEntry"];

export type RunUsageLedger = Schemas["RunUsageLedger"];

export type RunReplay = Schemas["RunReplay"];

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

export type DocumentInfo = {
  id: string;
  workspace_id?: string;
  title: string;
  source_type: string;
  source_uri?: string;
  mime_type?: string;
  metadata: Record<string, unknown>;
  chunk_count?: number;
  embedding_count?: number;
  created_at: string;
  updated_at: string;
};

export type RetrievedDocumentChunk = {
  document: DocumentInfo;
  chunk: {
    id: string;
    document_id: string;
    chunk_index: number;
    content: string;
    token_count: number;
    metadata: Record<string, unknown>;
    created_at: string;
  };
  similarity: number;
  recency_boost: number;
  score: number;
  vector_rank?: number;
  rerank_rank?: number;
  lexical_boost?: number;
  metadata_boost?: number;
  diversity_penalty?: number;
  rerank_score?: number;
  matched_terms?: string[];
  evidence_score?: number;
  evidence_coverage?: number;
  confidence?: "high" | "medium" | "low" | string;
  filter_reason?: string;
};

export type EmbeddingInfo = {
  provider: string;
  model: string;
  dimensions?: number;
  estimated: boolean;
};

export type DocumentSearchResponse = {
  items: RetrievedDocumentChunk[];
  embedding?: EmbeddingInfo;
  no_match?: boolean;
  reason?: string;
};

export type RAGEvaluationCase = {
  id: string;
  query: string;
  expected_document_ids?: string[];
  expected_chunk_ids?: string[];
  expected_chunk_contains?: string[];
  min_acceptable_rank?: number;
  tags?: string[];
};

export type RAGEvaluationRunResponse = {
  summary: {
    total: number;
    hit_at_1: number;
    hit_at_3: number;
    hit_at_5: number;
    misses: number;
  };
  cases: Array<{
    id: string;
    query: string;
    expected_document_ids?: string[];
    expected_chunk_ids?: string[];
    expected_chunk_contains?: string[];
    tags?: string[];
    hit: boolean;
    hit_at_1: boolean;
    hit_at_3: boolean;
    hit_at_5: boolean;
    best_rank?: number;
    failure_reason?: string;
    items: RetrievedDocumentChunk[];
  }>;
  embedding?: EmbeddingInfo;
};

export type DocumentDetail = {
  document: DocumentInfo;
  chunks: RetrievedDocumentChunk["chunk"][];
};

const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";
const contractClient = createClient<paths>({ baseUrl: API_BASE });

function contractError(action: string, response: Response, error: unknown): Error {
  const detail =
    error && typeof error === "object" && "error" in error && typeof error.error === "string"
      ? ` ${error.error}`
      : "";
  return new Error(`${action}: ${response.status}${detail}`);
}

async function readJSON<T>(response: Response): Promise<T> {
  return response.json() as Promise<T>;
}

async function readArrayJSON<T>(response: Response): Promise<T[]> {
  const data = await readJSON<unknown>(response);
  return Array.isArray(data) ? (data as T[]) : [];
}

function normalizeRunReplay(data: unknown): RunReplay {
  const replay = data as Partial<RunReplay>;
  const run = replay.run as RunInfo;
  return {
    run,
    conversation: replay.conversation as Conversation,
    summary: replay.summary as RunTraceSummary,
    messages: Array.isArray(replay.messages) ? replay.messages : [],
    steps: Array.isArray(replay.steps) ? replay.steps : [],
    usage_ledger: normalizeRunUsageLedger(replay.usage_ledger, run?.id ?? ""),
    run_events: Array.isArray(replay.run_events) ? replay.run_events : [],
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
  const ledger = value && typeof value === "object" ? (value as Partial<RunUsageLedger>) : {};
  return {
    run_id: typeof ledger.run_id === "string" ? ledger.run_id : runId,
    budget: ledger.budget && typeof ledger.budget === "object" ? ledger.budget : {},
    totals: {
      ...EMPTY_RUN_USAGE_TOTALS,
      ...(ledger.totals && typeof ledger.totals === "object" ? ledger.totals : {})
    },
    entries: Array.isArray(ledger.entries) ? ledger.entries : [],
    ...(typeof ledger.updated_at === "string" ? { updated_at: ledger.updated_at } : {})
  };
}

export async function listConversations(): Promise<Conversation[]> {
  const response = await fetch(`${API_BASE}/api/conversations`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load conversations: ${response.status}`);
  }
  return readArrayJSON<Conversation>(response);
}

export async function createConversation(title: string): Promise<Conversation> {
  const response = await fetch(`${API_BASE}/api/conversations`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title })
  });
  if (!response.ok) {
    throw new Error(`Failed to create conversation: ${response.status}`);
  }
  return readJSON<Conversation>(response);
}

export async function updateConversationTitle(conversationId: string, title: string): Promise<Conversation> {
  const response = await fetch(`${API_BASE}/api/conversations/${conversationId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title })
  });
  if (!response.ok) {
    throw new Error(`Failed to update conversation: ${response.status}`);
  }
  return readJSON<Conversation>(response);
}

export async function deleteConversation(conversationId: string): Promise<void> {
  const response = await fetch(`${API_BASE}/api/conversations/${conversationId}`, {
    method: "DELETE"
  });
  if (!response.ok) {
    throw new Error(`Failed to delete conversation: ${response.status}`);
  }
}

export async function listMessages(conversationId: string): Promise<Message[]> {
  const response = await fetch(`${API_BASE}/api/conversations/${conversationId}/messages`, {
    cache: "no-store"
  });
  if (!response.ok) {
    throw new Error(`Failed to load messages: ${response.status}`);
  }
  return readArrayJSON<Message>(response);
}

export async function streamChat(
  input: Schemas["ChatRequest"],
  onEvent: (event: ChatEvent) => void
) {
  const response = await fetch(`${API_BASE}/api/chat`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });

  if (!response.ok || !response.body) {
    const message = await response.text();
    throw new Error(`Chat request failed: ${response.status}${message ? ` ${message}` : ""}`);
  }

  await readChatEventStream(response, onEvent);
}

export async function continueRun(
  input: { run_id: string; plan: string },
  onEvent: (event: ChatEvent) => void
) {
  const response = await fetch(`${API_BASE}/api/runs/${input.run_id}/continue`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ plan: input.plan })
  });

  if (!response.ok || !response.body) {
    throw new Error(`Continue request failed: ${response.status}`);
  }

  await readChatEventStream(response, onEvent);
}

export async function resumeRun(
  input: { run_id: string; user_input: string },
  onEvent: (event: ChatEvent) => void
) {
  const response = await fetch(`${API_BASE}/api/runs/${input.run_id}/resume`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ user_input: input.user_input })
  });

  if (!response.ok || !response.body) {
    throw new Error(`Resume request failed: ${response.status}`);
  }

  await readChatEventStream(response, onEvent);
}

export async function cancelRun(runId: string): Promise<RunInfo> {
  const { data, error, response } = await contractClient.POST("/api/runs/{id}/cancel", {
    params: { path: { id: runId } }
  });
  if (!data) {
    throw contractError("Failed to cancel run", response, error);
  }
  return data;
}

export async function verifyRun(runId: string): Promise<Schemas["VerifyRunResponse"]> {
  const { data, error, response } = await contractClient.POST("/api/runs/{id}/verify", {
    params: { path: { id: runId } }
  });
  if (!data) {
    throw contractError("Verify request failed", response, error);
  }
  return data;
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
    agent_id: stringValue(payload.agent_id) ?? "", status: runStatusValue(payload.status) ?? fallbackRunStatus(event.type)
  };
  return { type: "model_delta", delta: "" };
}

function stringValue(value: unknown): string | undefined { return typeof value === "string" ? value : undefined; }
function numberValue(value: unknown): number | undefined { return typeof value === "number" ? value : undefined; }
function runStatusValue(value: unknown): Schemas["RunStatus"] | undefined {
  if (typeof value !== "string") return undefined;
  const statuses: Schemas["RunStatus"][] = [
    "queued", "running", "waiting_for_user", "completed", "failed", "failed_recoverable", "canceling", "canceled"
  ];
  return statuses.find((status) => status === value);
}
function fallbackRunStatus(eventType: string): Schemas["RunStatus"] {
  if (eventType === "run.created") return "queued";
  if (eventType === "run.waiting_for_user") return "waiting_for_user";
  if (eventType === "run.completed") return "completed";
  if (eventType === "run.failed") return "failed";
  if (eventType === "run.cancel_requested") return "canceling";
  if (eventType === "run.canceled") return "canceled";
  return "running";
}

export async function listAgents(): Promise<AgentInfo[]> {
  const { data, error, response } = await contractClient.GET("/api/agents", {
    fetch: (request) => fetch(request, { cache: "no-store" })
  });
  if (!data) {
    throw contractError("Failed to load agents", response, error);
  }
  return data.map(normalizeAgentInfo);
}

export async function createAgent(
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled" | "executor">>
): Promise<AgentInfo> {
  const { data, error, response } = await contractClient.POST("/api/agents", {
    body: input
  });
  if (!data) {
    throw contractError("Failed to create agent", response, error);
  }
  return normalizeAgentInfo(data);
}

export async function updateAgent(
  agentId: string,
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled" | "executor">>
): Promise<AgentInfo> {
  const { data, error, response } = await contractClient.PATCH("/api/agents/{id}", {
    params: { path: { id: agentId } },
    body: input
  });
  if (!data) {
    throw contractError("Failed to update agent", response, error);
  }
  return normalizeAgentInfo(data);
}

export async function archiveAgent(agentId: string): Promise<void> {
  const { error, response } = await contractClient.DELETE("/api/agents/{id}", {
    params: { path: { id: agentId } }
  });
  if (!response.ok) {
    throw contractError("Failed to archive agent", response, error);
  }
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
  const { data, error, response } = await contractClient.GET("/api/tools", {
    fetch: (request) => fetch(request, { cache: "no-store" })
  });
  if (!data) {
    throw contractError("Failed to load tools", response, error);
  }
  return data;
}

export async function listRuns(): Promise<RunInfo[]> {
  const { data, error, response } = await contractClient.GET("/api/runs", {
    fetch: (request) => fetch(request, { cache: "no-store" })
  });
  if (!data) {
    throw contractError("Failed to load runs", response, error);
  }
  return data;
}

export async function listCollaborationSteps(runId: string): Promise<CollaborationStepInfo[]> {
  const { data, error, response } = await contractClient.GET("/api/runs/{id}/collaboration_steps", {
    params: { path: { id: runId } },
    fetch: (request) => fetch(request, { cache: "no-store" })
  });
  if (!data) {
    throw contractError("Failed to load collaboration steps", response, error);
  }
  return data;
}

export async function getRunReplay(runId: string): Promise<RunReplay> {
  const { data, error, response } = await contractClient.GET("/api/runs/{id}/replay", {
    params: { path: { id: runId } },
    fetch: (request) => fetch(request, { cache: "no-store" })
  });
  if (!data) {
    throw contractError("Failed to load run replay", response, error);
  }
  return normalizeRunReplay(data);
}

export async function getRunUsage(runId: string): Promise<RunUsageLedger> {
  const { data, error, response } = await contractClient.GET("/api/runs/{id}/usage", {
    params: { path: { id: runId } },
    fetch: (request) => fetch(request, { cache: "no-store" })
  });
  if (!data) {
    throw contractError("Failed to load run usage", response, error);
  }
  return normalizeRunUsageLedger(data, runId);
}

export async function getEpisodeReport(runId: string): Promise<EpisodeReport> {
  const response = await fetch(`${API_BASE}/api/runs/${runId}/episode`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load episode report: ${response.status}`);
  }
  return readJSON<EpisodeReport>(response);
}

export async function setToolEnabled(name: string, enabled: boolean): Promise<ToolInfo[]> {
  const result = enabled
    ? await contractClient.POST("/api/tools/{name}/enable", { params: { path: { name } } })
    : await contractClient.POST("/api/tools/{name}/disable", { params: { path: { name } } });
  if (!result.data) {
    throw contractError("Failed to update tool", result.response, result.error);
  }
  return result.data;
}

export async function listDocuments(): Promise<DocumentInfo[]> {
  const response = await fetch(`${API_BASE}/api/documents`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load documents: ${response.status}`);
  }
  return readArrayJSON<DocumentInfo>(response);
}

export async function createDocument(input: {
  title: string;
  content: string;
  metadata?: Record<string, unknown>;
}): Promise<DocumentInfo> {
  const response = await fetch(`${API_BASE}/api/documents`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });
  if (!response.ok) {
    throw new Error(`Failed to create document: ${response.status}`);
  }
  return readJSON<DocumentInfo>(response);
}

export async function uploadDocument(input: { file: File; title?: string }): Promise<DocumentInfo> {
  const form = new FormData();
  form.append("file", input.file);
  if (input.title?.trim()) {
    form.append("title", input.title.trim());
  }
  const response = await fetch(`${API_BASE}/api/documents/upload`, {
    method: "POST",
    body: form
  });
  if (!response.ok) {
    throw new Error(`Failed to upload document: ${response.status}`);
  }
  return readJSON<DocumentInfo>(response);
}

export async function getDocument(documentId: string): Promise<DocumentDetail> {
  const response = await fetch(`${API_BASE}/api/documents/${documentId}`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load document: ${response.status}`);
  }
  const data = await readJSON<Partial<DocumentDetail>>(response);
  return {
    document: data.document as DocumentInfo,
    chunks: Array.isArray(data.chunks) ? data.chunks : []
  };
}

export async function deleteDocument(documentId: string): Promise<void> {
  const response = await fetch(`${API_BASE}/api/documents/${documentId}`, {
    method: "DELETE"
  });
  if (!response.ok) {
    throw new Error(`Failed to delete document: ${response.status}`);
  }
}

export async function searchRAG(input: {
  query: string;
  metadata?: Record<string, string>;
  limit?: number;
  min_similarity?: number;
}): Promise<DocumentSearchResponse> {
  const response = await fetch(`${API_BASE}/api/rag/search`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });
  if (!response.ok) {
    throw new Error(`Failed to search knowledge: ${response.status}`);
  }
  const data = await readJSON<unknown>(response);
  if (Array.isArray(data)) {
    return { items: data as RetrievedDocumentChunk[] };
  }
  const payload = data as Partial<DocumentSearchResponse>;
  return {
    items: Array.isArray(payload.items) ? payload.items : [],
    embedding: payload.embedding
  };
}

export async function runRAGEvaluation(input: {
  cases: RAGEvaluationCase[];
  top_k?: number;
  min_similarity?: number;
  metadata?: Record<string, string>;
}): Promise<RAGEvaluationRunResponse> {
  const response = await fetch(`${API_BASE}/api/rag/evaluations/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });
  if (!response.ok) {
    throw new Error(`Failed to run retrieval evaluation: ${response.status}`);
  }
  return readJSON<RAGEvaluationRunResponse>(response);
}
