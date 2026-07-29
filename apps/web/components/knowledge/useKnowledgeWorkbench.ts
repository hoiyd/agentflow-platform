"use client";

import type { ChangeEvent } from "react";
import { useState } from "react";
import {
  type ContextSelectionInfo,
  type DocumentDetail,
  type DocumentInfo,
  type EmbeddingInfo,
  type FusionInfo,
  type KnowledgeSecurityInfo,
  type RAGEvaluationCase,
  type RAGEvaluationRunResponse,
  type RetrievedDocumentChunk,
  createDocument,
  deleteDocument,
  getDocument,
  listDocuments,
  runRAGEvaluation,
  searchRAG,
  uploadDocument
} from "../../lib/knowledge-api";

const DEFAULT_RAG_EVAL_CASES = `[
  {
    "id": "example_resume_backend",
    "query": "候选人的后端系统设计经验",
    "expected_chunk_contains": ["Go", "PostgreSQL"],
    "min_acceptable_rank": 3,
    "tags": ["resume", "backend"]
  }
]`;

export function useKnowledgeWorkbench() {
  const [documents, setDocuments] = useState<DocumentInfo[]>([]);
  const [error, setError] = useState("");
  const [documentTitle, setDocumentTitle] = useState("");
  const [documentContent, setDocumentContent] = useState("");
  const [isCreating, setIsCreating] = useState(false);
  const [uploadTitle, setUploadTitle] = useState("");
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [isUploading, setIsUploading] = useState(false);
  const [query, setQuery] = useState("");
  const [minSimilarity, setMinSimilarity] = useState("0.15");
  const [contextTokenBudget, setContextTokenBudget] = useState("16000");
  const [results, setResults] = useState<RetrievedDocumentChunk[]>([]);
  const [contextItems, setContextItems] = useState<RetrievedDocumentChunk[]>([]);
  const [contextSelection, setContextSelection] = useState<ContextSelectionInfo | null>(null);
  const [searchEmbedding, setSearchEmbedding] = useState<EmbeddingInfo | null>(null);
  const [searchFusion, setSearchFusion] = useState<FusionInfo | null>(null);
  const [searchSecurity, setSearchSecurity] = useState<KnowledgeSecurityInfo | null>(null);
  const [noMatchReason, setNoMatchReason] = useState("");
  const [hasSearched, setHasSearched] = useState(false);
  const [isSearching, setIsSearching] = useState(false);
  const [evaluationCases, setEvaluationCases] = useState(DEFAULT_RAG_EVAL_CASES);
  const [evaluationResult, setEvaluationResult] = useState<RAGEvaluationRunResponse | null>(null);
  const [isRunningEvaluation, setIsRunningEvaluation] = useState(false);
  const [selectedDocument, setSelectedDocument] = useState<DocumentDetail | null>(null);
  const [selectedDocumentId, setSelectedDocumentId] = useState("");
  const [isLoadingDocumentDetail, setIsLoadingDocumentDetail] = useState(false);
  const [deletingDocumentId, setDeletingDocumentId] = useState("");

  async function refreshDocuments() {
    try {
      setError("");
      setDocuments(await listDocuments());
      setSelectedDocument(null);
      setSelectedDocumentId("");
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : "Failed to load documents");
    }
  }

  async function createTextDocument() {
    const title = documentTitle.trim();
    const content = documentContent.trim();
    if (!title || !content || isCreating) {
      return;
    }
    setIsCreating(true);
    setError("");
    try {
      const created = await createDocument({
        title,
        content,
        metadata: { source: "ui" }
      });
      setDocuments((items) => [created, ...items]);
      setDocumentTitle("");
      setDocumentContent("");
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : "Failed to create document");
    } finally {
      setIsCreating(false);
    }
  }

  function selectUploadFile(event: ChangeEvent<HTMLInputElement>) {
    setUploadFile(event.target.files?.[0] ?? null);
  }

  async function uploadKnowledgeDocument() {
    if (!uploadFile || isUploading) {
      return;
    }
    setIsUploading(true);
    setError("");
    try {
      const created = await uploadDocument({
        file: uploadFile,
        title: uploadTitle
      });
      setDocuments((items) => [created, ...items]);
      setUploadFile(null);
      setUploadTitle("");
    } catch (uploadError) {
      setError(uploadError instanceof Error ? uploadError.message : "Failed to upload document");
    } finally {
      setIsUploading(false);
    }
  }

  async function searchKnowledge() {
    const normalizedQuery = query.trim();
    if (!normalizedQuery || isSearching) {
      return;
    }
    setIsSearching(true);
    setHasSearched(true);
    setError("");
    try {
      const parsedMinSimilarity = Number(minSimilarity);
      const parsedContextTokenBudget = Number(contextTokenBudget);
      const response = await searchRAG({
        query: normalizedQuery,
        limit: 5,
        min_similarity: Number.isFinite(parsedMinSimilarity) ? parsedMinSimilarity : 0,
        context_token_budget:
          Number.isFinite(parsedContextTokenBudget) && parsedContextTokenBudget > 0
            ? Math.floor(parsedContextTokenBudget)
            : 16000
      });
      setResults(response.items);
      setContextItems(response.context_items ?? []);
      setContextSelection(response.context_selection ?? null);
      setSearchEmbedding(response.embedding ?? null);
      setSearchFusion(response.fusion ?? null);
      setSearchSecurity(response.security ?? null);
      setNoMatchReason(response.no_match ? response.reason ?? "No confident match found." : "");
    } catch (searchError) {
      setError(searchError instanceof Error ? searchError.message : "Failed to search knowledge");
    } finally {
      setIsSearching(false);
    }
  }

  async function runEvaluation() {
    if (isRunningEvaluation) {
      return;
    }
    setIsRunningEvaluation(true);
    setError("");
    try {
      const parsed = JSON.parse(evaluationCases) as unknown;
      if (!Array.isArray(parsed)) {
        throw new Error("Evaluation cases must be a JSON array");
      }
      const parsedMinSimilarity = Number(minSimilarity);
      const response = await runRAGEvaluation({
        cases: parsed as RAGEvaluationCase[],
        top_k: 5,
        min_similarity: Number.isFinite(parsedMinSimilarity) ? parsedMinSimilarity : 0
      });
      setEvaluationResult(response);
    } catch (evaluationError) {
      setError(evaluationError instanceof Error ? evaluationError.message : "Failed to run retrieval evaluation");
    } finally {
      setIsRunningEvaluation(false);
    }
  }

  async function selectDocument(documentId: string) {
    setSelectedDocumentId(documentId);
    setIsLoadingDocumentDetail(true);
    setError("");
    try {
      setSelectedDocument(await getDocument(documentId));
    } catch (selectionError) {
      setError(selectionError instanceof Error ? selectionError.message : "Failed to load document");
    } finally {
      setIsLoadingDocumentDetail(false);
    }
  }

  async function removeDocument(document: DocumentInfo) {
    if (deletingDocumentId) {
      return;
    }
    const confirmed = window.confirm(`Delete knowledge document "${document.title}"? This cannot be undone.`);
    if (!confirmed) {
      return;
    }
    setDeletingDocumentId(document.id);
    setError("");
    try {
      await deleteDocument(document.id);
      setDocuments((items) => items.filter((item) => item.id !== document.id));
      setResults((items) => items.filter((item) => item.document.id !== document.id));
      if (selectedDocumentId === document.id) {
        setSelectedDocumentId("");
        setSelectedDocument(null);
      }
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "Failed to delete document");
    } finally {
      setDeletingDocumentId("");
    }
  }

  return {
    documents,
    contextItems,
    contextSelection,
    contextTokenBudget,
    documentTitle,
    documentContent,
    deletingDocumentId,
    error,
    evaluationCases,
    evaluationResult,
    hasSearched,
    isCreating,
    isLoadingDocumentDetail,
    isRunningEvaluation,
    isSearching,
    isUploading,
    minSimilarity,
    noMatchReason,
    query,
    results,
    searchEmbedding,
    searchFusion,
    searchSecurity,
    selectedDocument,
    selectedDocumentId,
    uploadFile,
    uploadTitle,
    createTextDocument,
    refreshDocuments,
    removeDocument,
    runEvaluation,
    searchKnowledge,
    selectDocument,
    selectUploadFile,
    setDocumentContent,
    setDocumentTitle,
    setContextTokenBudget,
    setEvaluationCases,
    setMinSimilarity,
    setQuery,
    setUploadTitle,
    uploadKnowledgeDocument
  };
}

export type KnowledgeWorkbenchModel = ReturnType<typeof useKnowledgeWorkbench>;
