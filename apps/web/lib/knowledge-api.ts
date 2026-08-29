import { apiArray, apiJSON, apiObject, apiVoid, expectObject } from "./api-client.ts";

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

export type DocumentDetail = {
  document: DocumentInfo;
  chunks: RetrievedDocumentChunk["chunk"][];
};

export async function listDocuments(): Promise<DocumentInfo[]> {
  return apiArray<DocumentInfo>(
    "/api/documents",
    { cache: "no-store" },
    { errorMessage: "Failed to load documents" }
  );
}

export async function createDocument(input: {
  title: string;
  version?: string;
  content: string;
  metadata?: Record<string, unknown>;
}): Promise<DocumentInfo> {
  return apiObject<DocumentInfo>(
    "/api/documents",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Failed to create document" },
    "document"
  );
}

export async function uploadDocument(input: { file: File; title?: string }): Promise<DocumentInfo> {
  const form = new FormData();
  form.append("file", input.file);
  if (input.title?.trim()) {
    form.append("title", input.title.trim());
  }
  return apiObject<DocumentInfo>(
    "/api/documents/upload",
    { method: "POST", body: form },
    { errorMessage: "Failed to upload document" },
    "document"
  );
}

export async function getDocument(documentId: string): Promise<DocumentDetail> {
  const data = await apiObject<Record<string, unknown>>(
    `/api/documents/${documentId}`,
    { cache: "no-store" },
    { errorMessage: "Failed to load document" },
    "document detail"
  );
  const document = expectObject<DocumentInfo>(data.document, "document detail document");
  if (typeof document.id !== "string" || document.id === "") {
    throw new Error("Invalid document detail document response: id is required");
  }
  return {
    document,
    chunks: Array.isArray(data.chunks) ? data.chunks : []
  };
}

export async function deleteDocument(documentId: string): Promise<void> {
  return apiVoid(
    `/api/documents/${documentId}`,
    { method: "DELETE" },
    { errorMessage: "Failed to delete document" }
  );
}

export async function searchRAG(input: {
  query: string;
  metadata?: Record<string, string>;
  limit?: number;
  min_similarity?: number;
  knowledge_context_max_tokens?: number;
}): Promise<DocumentSearchResponse> {
  const data = await apiJSON(
    "/api/rag/search",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Failed to search knowledge" }
  );
  if (Array.isArray(data)) {
    return { items: data as RetrievedDocumentChunk[] };
  }
  const payload = expectObject<Partial<DocumentSearchResponse>>(data, "knowledge search");
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
