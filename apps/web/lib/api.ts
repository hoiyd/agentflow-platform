export type Conversation = {
  id: string;
  title: string;
  created_at: string;
  updated_at: string;
};

export type Message = {
  id: string;
  conversation_id: string;
  role: "user" | "assistant" | "system";
  content: string;
  created_at: string;
};

export type ChatEvent =
  | { type: "conversation"; conversation_id: string }
  | {
      type: "run";
      conversation_id: string;
      run_id: string;
      agent_id: string;
      status:
        | "idle"
        | "queued"
        | "running"
        | "waiting_for_user"
        | "completed"
        | "failed"
        | "canceling"
        | "canceled"
        | string;
    }
  | {
      type: "autonomous_progress";
      conversation_id: string;
      run_id: string;
      agent_id?: string;
      iteration?: number;
      max_iterations?: number;
      elapsed_seconds?: number;
      max_runtime_seconds?: number;
      output_chars?: number;
      max_output_chars?: number;
      tool_calls?: number;
      max_tool_calls?: number;
      stop_reason?: string;
    }
  | { type: "delta"; delta: string }
  | {
      type: "collaboration_step";
      conversation_id: string;
      run_id: string;
      agent_id?: string;
      role: "planner" | "router" | "worker" | "reviewer" | "finalizer" | string;
      status:
        | "idle"
        | "queued"
        | "running"
        | "waiting_for_user"
        | "completed"
        | "failed"
        | "canceling"
        | "canceled"
        | string;
      iteration?: number;
      input?: string;
      output?: string;
      error?: string;
    }
  | {
      type: "done";
      conversation_id: string;
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
        | "canceling"
        | "canceled"
        | string;
    }
  | { type: "error"; error: string };

export type AgentInfo = {
  id: string;
  name: string;
  description: string;
  system_prompt: string;
  tools: string[];
  created_at: string;
  updated_at: string;
};

export type ToolInfo = {
  name: string;
  description: string;
  parameters: Record<string, unknown>;
  source: "builtin" | "mcp" | string;
  source_id?: string;
  enabled: boolean;
};

export type ChatMode = "single" | "multi_agent" | "autonomous";

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
    | "canceling"
    | "canceled"
    | string;
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

export type TraceEventType = "llm_start" | "llm_end" | "tool_start" | "tool_end" | "error" | string;

export type TraceEventInfo = {
  id: string;
  run_id: string;
  step_id?: string;
  type: TraceEventType;
  payload: Record<string, unknown>;
  timestamp: string;
  duration_ms?: number;
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

export type RunReplay = {
  run: RunInfo;
  conversation: Conversation;
  messages: Message[];
  steps: CollaborationStepInfo[];
  summary: RunTraceSummary;
  events: TraceEventInfo[];
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
  return {
    run: replay.run as RunInfo,
    conversation: replay.conversation as Conversation,
    summary: replay.summary as RunTraceSummary,
    messages: Array.isArray(replay.messages) ? replay.messages : [],
    steps: Array.isArray(replay.steps) ? replay.steps : [],
    events: Array.isArray(replay.events) ? replay.events : []
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
  input: { conversation_id?: string; agent_id?: string; message: string; mode?: ChatMode },
  onEvent: (event: ChatEvent) => void
) {
  const response = await fetch(`${API_BASE}/api/chat`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });

  if (!response.ok || !response.body) {
    throw new Error(`Chat request failed: ${response.status}`);
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
      onEvent(JSON.parse(dataLine.slice(6)) as ChatEvent);
    }
  }
}

export async function listAgents(): Promise<AgentInfo[]> {
  const response = await fetch(`${API_BASE}/api/agents`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`Failed to load agents: ${response.status}`);
  }
  return readArrayJSON<AgentInfo>(response);
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
