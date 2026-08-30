"use client";

import { useState } from "react";

import {
  createMemory,
  searchMemories,
  type MemoryInfo,
  type RetrievedMemory
} from "../../lib/memory-api";

export function useMemoryWorkbench() {
  const [kind, setKind] = useState("fact");
  const [content, setContent] = useState("");
  const [query, setQuery] = useState("");
  const [limit, setLimit] = useState(10);
  const [results, setResults] = useState<RetrievedMemory[]>([]);
  const [lastCreated, setLastCreated] = useState<MemoryInfo | null>(null);
  const [error, setError] = useState("");
  const [hasSearched, setHasSearched] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  const [isSearching, setIsSearching] = useState(false);

  async function saveMemory() {
    const normalizedContent = content.trim();
    if (!normalizedContent || isSaving) {
      return;
    }
    setIsSaving(true);
    setError("");
    try {
      const created = await createMemory({
        kind,
        content: normalizedContent,
        metadata: { source: "manual_workbench" }
      });
      setLastCreated(created);
      setContent("");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "Failed to save memory");
    } finally {
      setIsSaving(false);
    }
  }

  async function search() {
    const normalizedQuery = query.trim();
    if (!normalizedQuery || isSearching) {
      return;
    }
    setIsSearching(true);
    setHasSearched(true);
    setError("");
    try {
      setResults(await searchMemories({ query: normalizedQuery, limit }));
    } catch (searchError) {
      setResults([]);
      setError(searchError instanceof Error ? searchError.message : "Failed to search memories");
    } finally {
      setIsSearching(false);
    }
  }

  return {
    content,
    error,
    hasSearched,
    isSaving,
    isSearching,
    kind,
    lastCreated,
    limit,
    query,
    results,
    saveMemory,
    search,
    setContent,
    setKind,
    setLimit,
    setQuery
  };
}

export type MemoryWorkbenchModel = ReturnType<typeof useMemoryWorkbench>;
