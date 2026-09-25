import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { DocumentDetail } from "../../lib/knowledge-api";
import { useKnowledgeWorkbench } from "./useKnowledgeWorkbench";

const knowledgeAPI = vi.hoisted(() => ({
  createDocument: vi.fn(),
  deleteDocument: vi.fn(),
  getDocument: vi.fn(),
  listDocuments: vi.fn(),
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

  act(() => { void result.current.documents.selectDocument("first"); });
  act(() => { void result.current.documents.selectDocument("second"); });
  await act(async () => { second.resolve(documentDetail("second")); await second.promise; });
  await act(async () => { first.resolve(documentDetail("first")); await first.promise; });

  expect(result.current.documents.selectedDocument?.document.id).toBe("second");
  expect(result.current.documents.isLoadingDocumentDetail).toBe(false);
});

it("does not show an old search after the query changes", async () => {
  const first = deferred<{ items: [] }>();
  const second = deferred<{ items: []; no_match: boolean; reason: string }>();
  knowledgeAPI.searchRAG.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
  const { result } = renderHook(() => useKnowledgeWorkbench());

  act(() => result.current.search.setQuery("first"));
  act(() => { void result.current.search.searchKnowledge(); });
  act(() => result.current.search.setQuery("second"));
  act(() => { void result.current.search.searchKnowledge(); });
  await act(async () => { second.resolve({ items: [], no_match: true, reason: "second result" }); await second.promise; });
  await act(async () => { first.resolve({ items: [] }); await first.promise; });

  expect(result.current.search.noMatchReason).toBe("second result");
  expect(result.current.search.isSearching).toBe(false);
});

it("ignores an older document refresh", async () => {
  const first = deferred<Awaited<ReturnType<typeof knowledgeAPI.listDocuments>>>();
  const second = deferred<Awaited<ReturnType<typeof knowledgeAPI.listDocuments>>>();
  knowledgeAPI.listDocuments.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
  const { result } = renderHook(() => useKnowledgeWorkbench());

  act(() => { void result.current.documents.refreshDocuments(); });
  act(() => { void result.current.documents.refreshDocuments(); });
  await act(async () => { second.resolve([documentDetail("second").document]); await second.promise; });
  await act(async () => { first.resolve([documentDetail("first").document]); await first.promise; });

  expect(result.current.documents.documents.map((item) => item.id)).toEqual(["second"]);
});

it("invalidates an in-flight search when its threshold changes", async () => {
  const search = deferred<{ items: [] }>();
  knowledgeAPI.searchRAG.mockReturnValue(search.promise);
  const { result } = renderHook(() => useKnowledgeWorkbench());

  act(() => result.current.search.setQuery("query"));
  act(() => { void result.current.search.searchKnowledge(); });
  act(() => result.current.search.setMinSimilarity("0.5"));
  await act(async () => { search.resolve({ items: [] }); await search.promise; });

  expect(result.current.search.hasSearched).toBe(false);
  expect(result.current.search.isSearching).toBe(false);
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
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((complete, fail) => { resolve = complete; reject = fail; });
  return { promise, resolve, reject };
}
