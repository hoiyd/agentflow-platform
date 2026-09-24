"use client";

import { useState, type SetStateAction } from "react";
import { useKnowledgeDocuments } from "./useKnowledgeDocuments";
import { useKnowledgeEvaluation } from "./useKnowledgeEvaluation";
import { useKnowledgeSearch } from "./useKnowledgeSearch";

export function useKnowledgeWorkbench() {
  const [minSimilarity, setMinSimilarityValue] = useState("0.15");
  const search = useKnowledgeSearch(minSimilarity, setMinSimilarityValue);
  const evaluation = useKnowledgeEvaluation(minSimilarity, setMinSimilarityValue);
  const documents = useKnowledgeDocuments((documentId) => {
    search.forgetDocument(documentId);
    evaluation.invalidate();
  });
  function setMinSimilarity(value: SetStateAction<string>) {
    search.invalidate();
    evaluation.invalidate();
    setMinSimilarityValue(value);
  }
  return {
    documents,
    search: { ...search, setMinSimilarity },
    evaluation: { ...evaluation, setMinSimilarity }
  };
}

export type KnowledgeWorkbenchModel = ReturnType<typeof useKnowledgeWorkbench>;
