import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { RAGEvaluationRunResponse } from "../../lib/knowledge-api";
import { useKnowledgeEvaluation } from "./useKnowledgeEvaluation";

const runRAGEvaluation = vi.hoisted(() => vi.fn());
vi.mock("../../lib/knowledge-api", () => ({ runRAGEvaluation }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("discards a pending evaluation when its threshold changes", async () => {
  let resolve!: (value: RAGEvaluationRunResponse) => void;
  runRAGEvaluation.mockReturnValue(new Promise<RAGEvaluationRunResponse>((complete) => { resolve = complete; }));
  const { result } = renderHook(() => useKnowledgeEvaluation());

  act(() => { void result.current.runEvaluation(); });
  act(() => result.current.setMinSimilarity("0.5"));
  await act(async () => resolve({
    summary: { total: 1, hit_at_1: 1, hit_at_3: 1, hit_at_5: 1, misses: 0 },
    cases: []
  }));

  expect(result.current.minSimilarity).toBe("0.5");
  expect(result.current.evaluationResult).toBeNull();
  expect(result.current.isRunningEvaluation).toBe(false);
});
