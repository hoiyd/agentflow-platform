"use client";

import { useEffect, useState, type Dispatch, type SetStateAction } from "react";
import { searchRAG, type DocumentSearchResponse } from "../../lib/knowledge-api";
import { createLatestRequestController } from "../../lib/latest-request";

export function useKnowledgeSearch(minSimilarity: string, setMinSimilarity: Dispatch<SetStateAction<string>>) {
  const [query, setQueryValue] = useState("");
  const [knowledgeContextMaxTokens, setContextTokenLimit] = useState("16000");
  const [response, setResponse] = useState<DocumentSearchResponse | null>(null);
  const [error, setError] = useState("");
  const [isSearching, setIsSearching] = useState(false);
  const [requests] = useState(createLatestRequestController);

  useEffect(() => () => requests.cancel(), [requests]);

  function invalidate() {
    requests.cancel();
    setIsSearching(false);
    setResponse(null);
    setError("");
  }

  function setQuery(value: string) {
    invalidate();
    setQueryValue(value);
  }

  function setKnowledgeContextMaxTokens(value: string) {
    invalidate();
    setContextTokenLimit(value);
  }

  async function searchKnowledge() {
    const normalizedQuery = query.trim();
    if (!normalizedQuery || isSearching) return;
    const request = requests.begin();
    setIsSearching(true);
    setError("");
    try {
      const parsedMinSimilarity = Number(minSimilarity);
      const parsedKnowledgeContextMaxTokens = Number(knowledgeContextMaxTokens);
      const next = await searchRAG({
        query: normalizedQuery,
        limit: 5,
        min_similarity: Number.isFinite(parsedMinSimilarity) ? parsedMinSimilarity : 0,
        knowledge_context_max_tokens:
          Number.isFinite(parsedKnowledgeContextMaxTokens) && parsedKnowledgeContextMaxTokens > 0
            ? Math.floor(parsedKnowledgeContextMaxTokens)
            : 16000
      }, request.signal);
      if (request.isCurrent()) setResponse(next);
    } catch (searchError) {
      if (request.isCurrent()) setError(searchError instanceof Error ? searchError.message : "Failed to search knowledge");
    } finally {
      if (request.isCurrent()) setIsSearching(false);
    }
  }

  function forgetDocument(documentId: string) {
    requests.cancel();
    setIsSearching(false);
    setResponse((current) => current ? {
      ...current,
      items: current.items.filter((item) => item.document.id !== documentId),
      context_items: current.context_items?.filter((item) => item.document.id !== documentId)
    } : current);
  }

  return {
    query, setQuery, minSimilarity, setMinSimilarity, knowledgeContextMaxTokens, setKnowledgeContextMaxTokens,
    results: response?.items ?? [], contextItems: response?.context_items ?? [],
    contextSelection: response?.context_selection ?? null, searchEmbedding: response?.embedding ?? null,
    searchFusion: response?.fusion ?? null, searchReranker: response?.reranker ?? null,
    searchRelevanceGate: response?.relevance_gate ?? null, searchSecurity: response?.security ?? null,
    noMatchReason: response?.no_match ? response.reason ?? "No confident match found." : "",
    hasSearched: response !== null, isSearching, error, searchKnowledge, forgetDocument, invalidate
  };
}

export type KnowledgeSearchModel = ReturnType<typeof useKnowledgeSearch>;
