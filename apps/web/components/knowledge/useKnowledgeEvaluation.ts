"use client";

import { useEffect, useState, type Dispatch, type SetStateAction } from "react";
import { runRAGEvaluation, type RAGEvaluationCase, type RAGEvaluationRunResponse, type RAGGoldenDataset } from "../../lib/knowledge-api";
import { createLatestRequestController } from "../../lib/latest-request";

const DEFAULT_RAG_EVAL_CASES = `{
  "schema_version": "rag-golden-dataset-v1",
  "id": "resume-retrieval-example",
  "version": "1.0.0",
  "tags": ["example"],
  "cases": [
    {
      "id": "example_resume_backend",
      "query": "候选人的后端系统设计经验",
      "answerable": true,
      "expected_sources": [
        {"content_contains": ["Go", "PostgreSQL"]}
      ],
      "min_acceptable_rank": 3,
      "tags": ["resume", "backend"]
    }
  ]
}`;

export function useKnowledgeEvaluation(minSimilarity: string, setMinSimilarity: Dispatch<SetStateAction<string>>) {
  const [evaluationCases, setEvaluationCasesValue] = useState(DEFAULT_RAG_EVAL_CASES);
  const [evaluationResult, setEvaluationResult] = useState<RAGEvaluationRunResponse | null>(null);
  const [isRunningEvaluation, setIsRunningEvaluation] = useState(false);
  const [error, setError] = useState("");
  const [requests] = useState(createLatestRequestController);

  useEffect(() => () => requests.cancel(), [requests]);

  function invalidate() {
    requests.cancel();
    setIsRunningEvaluation(false);
    setEvaluationResult(null);
    setError("");
  }

  function setEvaluationCases(value: string) {
    invalidate();
    setEvaluationCasesValue(value);
  }

  async function runEvaluation() {
    if (isRunningEvaluation) return;
    const request = requests.begin();
    setIsRunningEvaluation(true);
    setError("");
    try {
      const parsed = JSON.parse(evaluationCases) as unknown;
      const parsedMinSimilarity = Number(minSimilarity);
      const options = { top_k: 5, min_similarity: Number.isFinite(parsedMinSimilarity) ? parsedMinSimilarity : 0 };
      const result = Array.isArray(parsed)
        ? await runRAGEvaluation({ cases: parsed as RAGEvaluationCase[], ...options }, request.signal)
        : isGoldenDataset(parsed)
          ? await runRAGEvaluation({ dataset: parsed, ...options }, request.signal)
          : (() => { throw new Error("Evaluation input must be a Golden Dataset object or a legacy case array"); })();
      if (request.isCurrent()) setEvaluationResult(result);
    } catch (evaluationError) {
      if (request.isCurrent()) setError(evaluationError instanceof Error ? evaluationError.message : "Failed to run retrieval evaluation");
    } finally {
      if (request.isCurrent()) setIsRunningEvaluation(false);
    }
  }

  return { evaluationCases, setEvaluationCases, evaluationResult, isRunningEvaluation, minSimilarity, setMinSimilarity, error, runEvaluation, invalidate };
}

function isGoldenDataset(value: unknown): value is RAGGoldenDataset {
  if (!value || typeof value !== "object" || Array.isArray(value)) return false;
  const candidate = value as Partial<RAGGoldenDataset>;
  return candidate.schema_version === "rag-golden-dataset-v1" && typeof candidate.id === "string" && typeof candidate.version === "string" && Array.isArray(candidate.cases);
}

export type KnowledgeEvaluationModel = ReturnType<typeof useKnowledgeEvaluation>;
