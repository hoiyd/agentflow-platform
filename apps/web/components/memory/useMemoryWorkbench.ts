"use client";

import { useRef, useState } from "react";

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
  const searchGeneration = useRef(0);
  const [lastChanged, setLastChanged] = useState<MemoryInfo | null>(null);

  function applyMemoryChange(memory: MemoryInfo) {
    // A search started before the mutation must not restore stale content.
    searchGeneration.current++;
    setResults((items) => items.filter((item) => item.memory.id !== memory.id));
    setLastCreated((item) => item?.id === memory.id ? null : item);
    setLastChanged(memory);
  }

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
    const generation = ++searchGeneration.current;
    setHasSearched(true);
    setError("");
    try {
      const items = await searchMemories({ query: normalizedQuery, limit });
      if (generation === searchGeneration.current) setResults(items);
    } catch (searchError) {
      if (generation === searchGeneration.current) {
        setResults([]);
        setError(searchError instanceof Error ? searchError.message : "Failed to search memories");
      }
    } finally {
      setIsSearching(false);
    }
  }

  return {
    applyMemoryChange,
    lastChanged,
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
