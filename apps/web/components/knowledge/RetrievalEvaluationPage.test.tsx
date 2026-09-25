import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import RetrievalEvaluationPage from "../../app/evaluations/retrieval/page";

const runRAGEvaluation = vi.hoisted(() => vi.fn());
vi.mock("../../lib/knowledge-api", () => ({ runRAGEvaluation }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("runs a Golden Dataset against the current index from the dedicated page", async () => {
  runRAGEvaluation.mockResolvedValue({
    dataset: { id: "resume-retrieval-example", version: "1.0.0", schema_version: "rag-golden-dataset-v1" },
    summary: { total: 1, hit_at_1: 1, hit_at_3: 1, hit_at_5: 1, misses: 0 },
    cases: []
  });
  render(<RetrievalEvaluationPage />);

  expect(screen.getByRole("link", { name: "Back to Knowledge" }).getAttribute("href")).toBe("/workspace?view=knowledge");
  fireEvent.click(screen.getByRole("button", { name: "Run evaluation" }));

  await waitFor(() => expect(runRAGEvaluation).toHaveBeenCalledTimes(1));
  expect(runRAGEvaluation.mock.calls[0][0]).toMatchObject({
    dataset: { id: "resume-retrieval-example" }, top_k: 5, min_similarity: 0.15
  });
  expect(await screen.findByText("Dataset: resume-retrieval-example / 1.0.0 / rag-golden-dataset-v1")).toBeTruthy();
});

it("shows evaluation errors on the dedicated page", async () => {
  runRAGEvaluation.mockRejectedValue(new Error("Index unavailable"));
  render(<RetrievalEvaluationPage />);

  fireEvent.click(screen.getByRole("button", { name: "Run evaluation" }));

  expect(await screen.findByRole("alert")).toHaveProperty("textContent", "Index unavailable");
});
