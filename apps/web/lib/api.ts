import type { CompletionContractInput } from "./verification";

export type Conversation = {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
  started_at?: string;
  heartbeat_at?: string;
  completed_at?: string;
};

export type Message = {
  id: string;
  conversation_id: string;
  role: "user" | "assistant" | "system";
  content: string;
  created_at: string;
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
    }
  | { type: "error"; error: string };

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

export type RunReplay = {
  run: RunInfo;
  conversation: Conversation;
  messages: Message[];
  steps: CollaborationStepInfo[];
  summary: RunTraceSummary;
  usage_ledger: RunUsageLedger;
  run_events: RunEvent[];
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

export type DocumentInfo = {
  id: string;
  workspace_id?: string;
  title: string;
  version?: string;
  content_hash?: string;
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
    parent_id?: string;
    section_path?: string[];
    start_offset?: number;
    end_offset?: number;
    document_version?: string;
    content_hash?: string;
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
  lexical_rank?: number;
  lexical_score?: number;
  rrf_score?: number;
  fusion_rank?: number;
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

export type FusionInfo = {
  algorithm: string;
  version: string;
  rank_constant: number;
  dense_weight: number;
  lexical_weight: number;
};

export type KnowledgeSecurityDecision = {
  document_id: string;
  chunk_id: string;
  action: "blocked" | string;
  reasons: string[];
};

export type KnowledgeSecurityInfo = {
  policy_version: string;
  untrusted_context: boolean;
  checked_candidates: number;
  blocked_candidates: number;
  decisions?: KnowledgeSecurityDecision[];
};

export type DocumentSearchResponse = {
  items: RetrievedDocumentChunk[];
  embedding?: EmbeddingInfo;
  fusion?: FusionInfo;
  security?: KnowledgeSecurityInfo;
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
    blocked_candidates?: number;
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
    security?: KnowledgeSecurityInfo;
  }>;
  embedding?: EmbeddingInfo;
  fusion?: FusionInfo;
};

export type DocumentDetail = {
  document: DocumentInfo;
  chunks: RetrievedDocumentChunk["chunk"][];
};

const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";

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
  const response = await fetch(`${API_BASE}/api/runs/${runId}/cancel`, {
    method: "POST"
  });
  if (!response.ok) {
    throw new Error(`Failed to cancel run: ${response.status}`);
  }
  return readJSON<RunInfo>(response);
}

export async function verifyRun(runId: string): Promise<{ run: RunInfo; decision: Record<string, unknown> }> {
  const response = await fetch(`${API_BASE}/api/runs/${runId}/verify`, { method: "POST" });
  if (!response.ok) {
    throw new Error(`Verify request failed: ${response.status}`);
  }
  return readJSON<{ run: RunInfo; decision: Record<string, unknown> }>(response);
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
  const response = await fetch(`${API_BASE}/api/agents`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load agents: ${response.status}`);
  }
  const agents = await readArrayJSON<AgentInfo>(response);
  return agents.map(normalizeAgentInfo);
}

export async function createAgent(
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled" | "executor">>
): Promise<AgentInfo> {
  const response = await fetch(`${API_BASE}/api/agents`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(`Failed to create agent: ${response.status}${message ? ` ${message}` : ""}`);
  }
  return normalizeAgentInfo(await readJSON<AgentInfo>(response));
}

export async function updateAgent(
  agentId: string,
  input: Partial<Pick<AgentInfo, "name" | "description" | "system_prompt" | "tools" | "memory_enabled" | "retrieval_enabled" | "executor">>
): Promise<AgentInfo> {
  const response = await fetch(`${API_BASE}/api/agents/${agentId}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(`Failed to update agent: ${response.status}${message ? ` ${message}` : ""}`);
  }
  return normalizeAgentInfo(await readJSON<AgentInfo>(response));
}

export async function archiveAgent(agentId: string): Promise<void> {
  const response = await fetch(`${API_BASE}/api/agents/${agentId}`, {
    method: "DELETE"
  });
  if (!response.ok) {
    const message = await response.text();
    throw new Error(`Failed to archive agent: ${response.status}${message ? ` ${message}` : ""}`);
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
  const response = await fetch(`${API_BASE}/api/tools`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load tools: ${response.status}`);
  }
  return readArrayJSON<ToolInfo>(response);
}

export async function listRuns(): Promise<RunInfo[]> {
  const response = await fetch(`${API_BASE}/api/runs`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load runs: ${response.status}`);
  }
  return readArrayJSON<RunInfo>(response);
}

export async function listCollaborationSteps(runId: string): Promise<CollaborationStepInfo[]> {
  const response = await fetch(`${API_BASE}/api/runs/${runId}/collaboration_steps`, {
    cache: "no-store"
  });
  if (!response.ok) {
    throw new Error(`Failed to load collaboration steps: ${response.status}`);
  }
  return readArrayJSON<CollaborationStepInfo>(response);
}

export async function getRunReplay(runId: string): Promise<RunReplay> {
  const response = await fetch(`${API_BASE}/api/runs/${runId}/replay`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load run replay: ${response.status}`);
  }
  return normalizeRunReplay(await readJSON<unknown>(response));
}

export async function getRunUsage(runId: string): Promise<RunUsageLedger> {
  const response = await fetch(`${API_BASE}/api/runs/${runId}/usage`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load run usage: ${response.status}`);
  }
  return normalizeRunUsageLedger(await readJSON<unknown>(response), runId);
}

export async function getEpisodeReport(runId: string): Promise<EpisodeReport> {
  const response = await fetch(`${API_BASE}/api/runs/${runId}/episode`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load episode report: ${response.status}`);
  }
  return readJSON<EpisodeReport>(response);
}

export async function setToolEnabled(name: string, enabled: boolean): Promise<ToolInfo[]> {
  const action = enabled ? "enable" : "disable";
  const response = await fetch(`${API_BASE}/api/tools/${encodeURIComponent(name)}/${action}`, {
    method: "POST"
  });
  if (!response.ok) {
    throw new Error(`Failed to update tool: ${response.status}`);
  }
  return readArrayJSON<ToolInfo>(response);
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
  version?: string;
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
    embedding: payload.embedding,
    fusion: payload.fusion,
    security: payload.security,
    no_match: payload.no_match,
    reason: payload.reason
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
