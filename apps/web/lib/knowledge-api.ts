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
  context_role?: "matched_child" | "parent" | "adjacent" | string;
  matched_chunk_id?: string;
  source_chunk_ids?: string[];
  matched_chunk_ids?: string[];
  merged_chunk_count?: number;
  source_id?: string;
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

export type RerankerInfo = {
  algorithm: string;
  version: string;
  config_version: string;
  provider?: string;
  model?: string;
};

export type RelevanceGateInfo = {
  policy: string;
  version: string;
  config_version: string;
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

export type ContextSelectionInfo = {
  version: string;
  max_tokens: number;
  tokens_used: number;
  matched_children: number;
  parent_chunks: number;
  adjacent_chunks: number;
  scope_filtered: boolean;
  transformation?: ContextTransformationInfo;
};

/**
 * Versioned post-selection context shaping metadata. The intentionally broad
 * name leaves room for future compression, truncation, or reordering stages.
 */
export type ContextTransformationInfo = {
  version: string;
  input_chunks: number;
  output_chunks: number;
  duplicates_removed: number;
  adjacent_merges: number;
  document_groups: number;
};

export type DocumentSearchResponse = {
  items: RetrievedDocumentChunk[];
  context_items?: RetrievedDocumentChunk[];
  citation_sources?: RAGCitation[];
  context_selection?: ContextSelectionInfo;
  embedding?: EmbeddingInfo;
  fusion?: FusionInfo;
  reranker?: RerankerInfo;
  relevance_gate?: RelevanceGateInfo;
  security?: KnowledgeSecurityInfo;
  no_match?: boolean;
  reason?: string;
};

export type RAGEvaluationCase = {
  id: string;
  query: string;
  answerable?: boolean;
  expected_sources?: RAGGoldenSource[];
  forbidden_sources?: RAGGoldenSource[];
  required_source_count?: number;
  expected_document_ids?: string[];
  expected_chunk_ids?: string[];
  expected_chunk_contains?: string[];
  min_acceptable_rank?: number;
  tags?: string[];
};

export type RAGGoldenSource = {
  document_id?: string;
  chunk_id?: string;
  source_uri?: string;
  content_contains?: string[];
};

type RAGGoldenCaseBase = {
  id: string;
  query: string;
  forbidden_sources?: RAGGoldenSource[];
  required_source_count?: number;
  min_acceptable_rank?: number;
  tags?: string[];
};

export type RAGGoldenCase = RAGGoldenCaseBase &
  (
    | { answerable: true; expected_sources: [RAGGoldenSource, ...RAGGoldenSource[]] }
    | { answerable: false; expected_sources?: never }
  );

export type RAGGoldenDataset = {
  schema_version: "rag-golden-dataset-v1";
  id: string;
  version: string;
  description?: string;
  tags?: string[];
  cases: RAGGoldenCase[];
};

export type RAGGoldenDatasetInfo = Omit<RAGGoldenDataset, "cases">;

export type RAGEvaluationRunResponse = {
  dataset?: RAGGoldenDatasetInfo;
  summary: {
    total: number;
    answerable_cases?: number;
    unanswerable_cases?: number;
    hit_at_1: number;
    hit_at_3: number;
    hit_at_5: number;
    misses: number;
    blocked_candidates?: number;
  };
  cases: Array<{
    id: string;
    query: string;
    answerable?: boolean;
    expected_sources?: RAGGoldenSource[];
    forbidden_sources?: RAGGoldenSource[];
    required_source_count?: number;
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
  reranker?: RerankerInfo;
  relevance_gate?: RelevanceGateInfo;
};

export type DocumentDetail = {
  document: DocumentInfo;
  chunks: RetrievedDocumentChunk["chunk"][];
};

const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";
const CONFIGURED_WORKSPACE_ID = process.env.NEXT_PUBLIC_WORKSPACE_ID?.trim();
const WORKSPACE_ID = !CONFIGURED_WORKSPACE_ID || CONFIGURED_WORKSPACE_ID === "default" ? "default_workspace" : CONFIGURED_WORKSPACE_ID;

function workspaceFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("X-Workspace-ID", WORKSPACE_ID);
  return fetch(input, { ...init, headers });
}

async function readJSON<T>(response: Response): Promise<T> {
  return response.json() as Promise<T>;
}

async function readArrayJSON<T>(response: Response): Promise<T[]> {
  const data = await readJSON<unknown>(response);
  return Array.isArray(data) ? (data as T[]) : [];
}

export async function listDocuments(): Promise<DocumentInfo[]> {
  const response = await workspaceFetch(`${API_BASE}/api/documents`, { cache: "no-store" });
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
  const response = await workspaceFetch(`${API_BASE}/api/documents`, {
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
  const response = await workspaceFetch(`${API_BASE}/api/documents/upload`, {
    method: "POST",
    body: form
  });
  if (!response.ok) {
    throw new Error(`Failed to upload document: ${response.status}`);
  }
  return readJSON<DocumentInfo>(response);
}

export async function getDocument(documentId: string): Promise<DocumentDetail> {
  const response = await workspaceFetch(`${API_BASE}/api/documents/${documentId}`, { cache: "no-store" });
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
  const response = await workspaceFetch(`${API_BASE}/api/documents/${documentId}`, {
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
  knowledge_context_max_tokens?: number;
}): Promise<DocumentSearchResponse> {
  const response = await workspaceFetch(`${API_BASE}/api/rag/search`, {
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
    context_items: Array.isArray(payload.context_items) ? payload.context_items : [],
    citation_sources: Array.isArray(payload.citation_sources) ? payload.citation_sources : [],
    context_selection: payload.context_selection,
    embedding: payload.embedding,
    fusion: payload.fusion,
    reranker: payload.reranker,
    relevance_gate: payload.relevance_gate,
    security: payload.security,
    no_match: payload.no_match,
    reason: payload.reason
  };
}

type RAGEvaluationRunInput = (
  | { dataset: RAGGoldenDataset; cases?: never }
  | { cases: RAGEvaluationCase[]; dataset?: never }
) & {
  top_k?: number;
  min_similarity?: number;
  metadata?: Record<string, string>;
};

export async function runRAGEvaluation(input: RAGEvaluationRunInput): Promise<RAGEvaluationRunResponse> {
  const response = await workspaceFetch(`${API_BASE}/api/rag/evaluations/run`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input)
  });
  if (!response.ok) {
    throw new Error(`Failed to run retrieval evaluation: ${response.status}`);
  }
  return readJSON<RAGEvaluationRunResponse>(response);
}
