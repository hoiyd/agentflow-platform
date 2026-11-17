import { apiArray, apiJSON, apiObject, apiVoid, expectObject } from "./api-client.ts";
import type { components } from "./api-contract.gen.ts";

export type DocumentInfo = components["schemas"]["KnowledgeDocument"];

export type RetrievedDocumentChunk = components["schemas"]["RetrievedDocumentChunk"];
export type RAGCitation = components["schemas"]["RAGCitation"];
export type EmbeddingInfo = components["schemas"]["EmbeddingInfo"];
export type FusionInfo = components["schemas"]["FusionInfo"];
export type RerankerInfo = components["schemas"]["RerankerInfo"];
export type RelevanceGateInfo = components["schemas"]["RelevanceGateInfo"];
export type RelevanceGateDecision = components["schemas"]["RelevanceGateDecision"];
export type KnowledgeSecurityInfo = components["schemas"]["KnowledgeSecurityInfo"];
export type ContextSelectionInfo = components["schemas"]["ContextSelectionInfo"];
export type DocumentSearchResponse = Omit<components["schemas"]["DocumentSearchResponse"], "items"> & {
  items: RetrievedDocumentChunk[];
};

export type RAGEvaluationCase = components["schemas"]["RAGEvaluationCase"];
export type RAGGoldenSource = components["schemas"]["RAGGoldenSource"];
type RAGGoldenCaseBase = Omit<RAGEvaluationCase, "answerable" | "expected_sources"> & { id: string };

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
    evidence?: components["schemas"]["RAGEvaluationEvidence"];
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
  metadata_schema?: components["schemas"]["DocumentMetadataSchema"];
};

export async function listDocuments(signal?: AbortSignal): Promise<DocumentInfo[]> {
  return apiArray<DocumentInfo>(
    "/api/documents",
    { cache: "no-store", signal },
    { errorMessage: "Failed to load documents" }
  );
}

export async function createDocument(input: components["schemas"]["DocumentIngestRequest"]): Promise<DocumentInfo> {
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

export async function updateDocumentMetadata(
  documentId: string,
  input: components["schemas"]["DocumentMetadataUpdate"],
  signal?: AbortSignal
): Promise<components["schemas"]["KnowledgeDocument"]> {
  return apiObject<components["schemas"]["KnowledgeDocument"]>(
    `/api/documents/${encodeURIComponent(documentId)}/metadata`,
    { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input), signal },
    { errorMessage: "Failed to update document metadata" },
    "document"
  );
}

export async function uploadDocument(input: Omit<components["schemas"]["DocumentUploadRequest"], "file" | "metadata"> & {
  file: File; metadata?: Record<string, unknown>;
}): Promise<DocumentInfo> {
  const form = new FormData();
  form.append("file", input.file);
  if (input.metadata_schema_version) form.append("metadata_schema_version", input.metadata_schema_version);
  if (input.metadata) form.append("metadata", JSON.stringify(input.metadata));
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

export async function getDocument(documentId: string, signal?: AbortSignal): Promise<DocumentDetail> {
  const data = await apiObject<Record<string, unknown>>(
    `/api/documents/${documentId}`,
    { cache: "no-store", signal },
    { errorMessage: "Failed to load document" },
    "document detail"
  );
  const document = expectObject<DocumentInfo>(data.document, "document detail document");
  if (typeof document.id !== "string" || document.id === "") {
    throw new Error("Invalid document detail document response: id is required");
  }
  return {
    document,
    chunks: Array.isArray(data.chunks) ? data.chunks : [],
    ...(data.metadata_schema ? { metadata_schema: expectObject<NonNullable<DocumentDetail["metadata_schema"]>>(data.metadata_schema, "document metadata schema") } : {})
  };
}

export async function deleteDocument(documentId: string): Promise<void> {
  return apiVoid(
    `/api/documents/${documentId}`,
    { method: "DELETE" },
    { errorMessage: "Failed to delete document" }
  );
}

export async function searchRAG(input: components["schemas"]["KnowledgeSearchRequest"], signal?: AbortSignal): Promise<DocumentSearchResponse> {
  const data = await apiJSON(
    "/api/rag/search",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
      signal
    },
    { errorMessage: "Failed to search knowledge" }
  );
  if (Array.isArray(data)) {
    return { items: data as RetrievedDocumentChunk[] };
  }
  const payload = expectObject<Partial<DocumentSearchResponse>>(data, "knowledge search");
  return {
    ...payload,
    items: Array.isArray(payload.items) ? payload.items : [],
    context_items: Array.isArray(payload.context_items) ? payload.context_items : [],
    citation_sources: Array.isArray(payload.citation_sources) ? payload.citation_sources : [],
    relevance_decisions: Array.isArray(payload.relevance_decisions) ? payload.relevance_decisions : [],
  };
}

type RAGEvaluationRunInput = {
  dataset: RAGGoldenDataset;
  top_k?: number;
  min_similarity?: number;
  metadata_filter?: components["schemas"]["MetadataFilter"];
};

export async function runRAGEvaluation(input: RAGEvaluationRunInput, signal?: AbortSignal): Promise<RAGEvaluationRunResponse> {
  return apiObject<RAGEvaluationRunResponse>(
    "/api/rag/evaluations/run",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
      signal
    },
    { errorMessage: "Failed to run retrieval evaluation" },
    "retrieval evaluation"
  );
}
