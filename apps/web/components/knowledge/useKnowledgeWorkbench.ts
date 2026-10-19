"use client";

import { useState, type SetStateAction } from "react";
import { useKnowledgeDocuments } from "./useKnowledgeDocuments";
import { useKnowledgeSearch } from "./useKnowledgeSearch";
import { useKnowledgeScope, type ScopeValues } from "./useKnowledgeScope";

export function useKnowledgeWorkbench() {
  const [minSimilarity, setMinSimilarityValue] = useState("0.15");
  const scope = useKnowledgeScope();
  const search = useKnowledgeSearch(minSimilarity, setMinSimilarityValue, scope.filter);
  const documents = useKnowledgeDocuments((documentId) => {
    search.forgetDocument(documentId);
  });
  function setMinSimilarity(value: SetStateAction<string>) {
    search.invalidate();
    setMinSimilarityValue(value);
  }
  return {
    scope: { ...scope, change: (values: ScopeValues) => { search.invalidate(); scope.change(values); } },
    documents,
    search: { ...search, setMinSimilarity, scopeValues: scope.values, onScopeChange: (values: ScopeValues) => { search.invalidate(); scope.change(values); } }
  };
}

export type KnowledgeWorkbenchModel = ReturnType<typeof useKnowledgeWorkbench>;
