import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { DocumentDetail } from "../../lib/knowledge-api";
import { useKnowledgeWorkbench } from "./useKnowledgeWorkbench";

const knowledgeAPI = vi.hoisted(() => ({
  createDocument: vi.fn(),
  deleteDocument: vi.fn(),
  getDocument: vi.fn(),
  listDocuments: vi.fn(),
  runRAGEvaluation: vi.fn(),
  searchRAG: vi.fn(),
  uploadDocument: vi.fn()
}));

vi.mock("../../lib/knowledge-api", () => knowledgeAPI);

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("keeps the latest document detail when requests finish out of order", async () => {
  const first = deferred<DocumentDetail>();
  const second = deferred<DocumentDetail>();
  knowledgeAPI.getDocument.mockImplementation((id: string) => id === "first" ? first.promise : second.promise);
  const { result } = renderHook(() => useKnowledgeWorkbench());

  act(() => { void result.current.selectDocument("first"); });
  act(() => { void result.current.selectDocument("second"); });
  await act(async () => { second.resolve(documentDetail("second")); await second.promise; });
  await act(async () => { first.resolve(documentDetail("first")); await first.promise; });

  expect(result.current.selectedDocument?.document.id).toBe("second");
  expect(result.current.isLoadingDocumentDetail).toBe(false);
});

function documentDetail(id: string): DocumentDetail {
  return {
    document: {
      id,
      workspace_id: "default_workspace",
      title: id,
      source_type: "text",
      metadata: {},
      created_at: "2026-09-10T00:00:00Z",
      updated_at: "2026-09-10T00:00:00Z"
    },
    chunks: []
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((complete) => { resolve = complete; });
  return { promise, resolve };
}
