import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { KnowledgePanel } from "./KnowledgePanel";
import type { KnowledgeWorkbenchModel } from "./useKnowledgeWorkbench";

afterEach(cleanup);

describe("KnowledgePanel navigation", () => {
  it("separates search, document management, and evaluation tasks", () => {
    render(<KnowledgePanel model={modelFixture()} />);

    expect(screen.getByRole("textbox", { name: "Search indexed knowledge" })).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: /Documents/ }));
    expect(screen.getByText("Add document")).toBeTruthy();
    expect(screen.queryByRole("textbox", { name: "Search indexed knowledge" })).toBeNull();

    fireEvent.click(screen.getByRole("tab", { name: "Paste text" }));
    expect(screen.getByRole("textbox", { name: "Document content" })).toBeTruthy();

    fireEvent.click(screen.getByRole("tab", { name: "Evaluation" }));
    expect(screen.getByText("Golden dataset")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Run evaluation" })).toBeTruthy();
  });
});

function modelFixture() {
  const noop = vi.fn();
  return {
    documents: {
      documents: [], documentTitle: "", documentContent: "", deletingDocumentId: "", error: "",
      isCreating: false, isLoadingDocumentDetail: false, isUploading: false,
      selectedDocument: null, selectedDocumentId: "", uploadFile: null, uploadTitle: "",
      createTextDocument: noop, refreshDocuments: noop, removeDocument: noop, selectDocument: noop,
      selectUploadFile: noop, setDocumentContent: noop, setDocumentTitle: noop, setUploadTitle: noop,
      uploadKnowledgeDocument: noop
    },
    search: {
      contextItems: [], contextSelection: null, knowledgeContextMaxTokens: "16000", error: "",
      hasSearched: false, isSearching: false, minSimilarity: "0.15", noMatchReason: "", query: "",
      results: [], searchEmbedding: null, searchFusion: null, searchReranker: null,
      searchRelevanceGate: null, searchSecurity: null, searchKnowledge: noop,
      setKnowledgeContextMaxTokens: noop, setMinSimilarity: noop, setQuery: noop
    },
    evaluation: {
      evaluationCases: "[]", evaluationResult: null, isRunningEvaluation: false,
      minSimilarity: "0.15", error: "", runEvaluation: noop, setEvaluationCases: noop,
      setMinSimilarity: noop
    }
  } as unknown as KnowledgeWorkbenchModel;
}
