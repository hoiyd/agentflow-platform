"use client";

import { useState, type SetStateAction } from "react";
import { useKnowledgeDocuments } from "./useKnowledgeDocuments";
import { useKnowledgeSearch } from "./useKnowledgeSearch";

export function useKnowledgeWorkbench() {
  const [minSimilarity, setMinSimilarityValue] = useState("0.15");
  const search = useKnowledgeSearch(minSimilarity, setMinSimilarityValue);
  const documents = useKnowledgeDocuments((documentId) => {
    search.forgetDocument(documentId);
  });
  function setMinSimilarity(value: SetStateAction<string>) {
    search.invalidate();
    setMinSimilarityValue(value);
  }
  return {
    documents,
    search: { ...search, setMinSimilarity }
  };
}

export type KnowledgeWorkbenchModel = ReturnType<typeof useKnowledgeWorkbench>;
