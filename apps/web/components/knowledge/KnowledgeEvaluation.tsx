import type { RAGEvaluationRunResponse } from "../../lib/knowledge-api";
import type { KnowledgeEvaluationModel } from "./useKnowledgeEvaluation";
import { formatPercent, KnowledgeResultCard, RetrievalDiagnostics } from "./KnowledgeRetrieval";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";

export function KnowledgeEvaluation({ model }: { model: KnowledgeEvaluationModel }) {
  const readOnly = useWorkspaceReadOnly();
  return (
    <section className="rag-evaluation">
      <div className="evaluation-controls">
        <label className="evaluation-dataset-label" htmlFor="evaluation-cases">Golden dataset</label>
        <label className="threshold-input">
          <span>Min similarity</span>
          <input
            max="1"
            min="0"
            onChange={(event) => model.setMinSimilarity(event.target.value)}
            step="0.05"
            type="number"
            value={model.minSimilarity}
          />
        </label>
        <button
          className="send compact-send"
          disabled={readOnly || model.isRunningEvaluation || model.evaluationCases.trim().length === 0}
          onClick={model.runEvaluation}
          type="button"
        >
          {model.isRunningEvaluation ? "Running..." : "Run evaluation"}
        </button>
      </div>
      <textarea
        className="evaluation-cases-input"
        id="evaluation-cases"
        onChange={(event) => model.setEvaluationCases(event.target.value)}
        spellCheck={false}
        value={model.evaluationCases}
      />
      {model.error ? <div className="knowledge-error" role="alert">{model.error}</div> : null}
      <EvaluationResult result={model.evaluationResult} />
    </section>
  );
}

function EvaluationResult({ result }: { result: RAGEvaluationRunResponse | null }) {
  if (!result) return <div className="knowledge-empty">Run evaluation to see hit@k and missed retrieval cases.</div>;

  const answerableTotal = result.summary.answerable_cases ?? result.summary.total;
  const hitRateDenominator = answerableTotal || 1;
  const evidence = result.cases.flatMap(item => item.evidence ? [item.evidence] : []);
  const expectedSources = evidence.reduce((total, item) => total + item.expected_sources, 0);
  const matchedSources = evidence.reduce((total, item) => total + item.matched_sources, 0);
  const contextLeaks = evidence.reduce((total, item) => total + item.context_leak_count, 0);
  return (
    <div className="evaluation-result">
      {result.dataset ? (
        <div className="evaluation-dataset">Dataset: {result.dataset.id} / {result.dataset.version} / {result.dataset.schema_version}</div>
      ) : null}
      <div className="evaluation-summary">
        <EvaluationMetric label="Total" value={String(result.summary.total)} />
        <EvaluationMetric label="Hit@1" value={formatPercent(result.summary.hit_at_1 / hitRateDenominator)} />
        <EvaluationMetric label="Hit@3" value={formatPercent(result.summary.hit_at_3 / hitRateDenominator)} />
        <EvaluationMetric label="Hit@5" value={formatPercent(result.summary.hit_at_5 / hitRateDenominator)} />
        <EvaluationMetric danger={result.summary.misses > 0} label="Misses" value={String(result.summary.misses)} />
        <EvaluationMetric danger={(result.summary.blocked_candidates ?? 0) > 0} label="Blocked" value={String(result.summary.blocked_candidates ?? 0)} />
        {evidence.length > 0 ? <>
          <EvaluationMetric label="Eligible source recall" value={expectedSources > 0 ? formatPercent(matchedSources / expectedSources) : "Not applicable"} />
          <EvaluationMetric danger={contextLeaks > 0} label="Context leaks" value={String(contextLeaks)} />
        </> : null}
      </div>
      <RetrievalDiagnostics
        embedding={result.embedding ?? null}
        fusion={result.fusion ?? null}
        gate={result.relevance_gate ?? null}
        hasSearched
        reranker={result.reranker ?? null}
      />
      <div className="evaluation-cases">
        {result.cases.map((item) => (
          <article className={item.hit ? "evaluation-case hit" : "evaluation-case miss"} key={item.id}>
            <div className="evaluation-case-header">
              <div>
                <h3>{item.id}</h3>
                <div className="tool-source">{item.query}</div>
              </div>
              <div className="document-metrics">
                <span>{item.hit ? (item.answerable === false ? "correct no-answer" : "hit") : "miss"}</span>
                <span>{item.best_rank ? `rank ${item.best_rank}` : item.answerable === false && item.hit ? "no result" : "no match"}</span>
              </div>
            </div>
            <div className="evaluation-expected">{evaluationExpectedLabel(item)}</div>
            {item.evidence ? <>
              <div className="document-metrics evaluation-evidence-metrics">
                <span>Sources {item.evidence.matched_sources}/{item.evidence.expected_sources}</span>
                <span>Context sources {item.evidence.context_matched_sources}/{item.evidence.expected_sources}</span>
                <span>{item.evidence.context_tokens} estimated Context tokens</span>
                <span>{item.evidence.latency_ms} ms</span>
              </div>
              <details className="evaluation-evidence">
                <summary>Applied constraints and source revisions</summary>
                <pre>{JSON.stringify(item.evidence, null, 2)}</pre>
              </details>
            </> : null}
            {item.failure_reason ? <div className="evaluation-failure">{item.failure_reason}</div> : null}
            {item.security && item.security.blocked_candidates > 0 ? (
              <div className="evaluation-failure">Knowledge security blocked {item.security.blocked_candidates} of {item.security.checked_candidates} candidates.</div>
            ) : null}
            <div className="rag-results compact-results">
              {item.items.slice(0, 5).map((resultItem) => <KnowledgeResultCard key={resultItem.chunk.id} result={resultItem} />)}
            </div>
          </article>
        ))}
      </div>
    </div>
  );
}

function EvaluationMetric({ danger = false, label, value }: { danger?: boolean; label: string; value: string }) {
  return (
    <div className={danger ? "evaluation-metric danger" : "evaluation-metric"}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function evaluationExpectedLabel(item: RAGEvaluationRunResponse["cases"][number]) {
  const expectedSources = item.expected_sources?.map(goldenSourceLabel).filter(Boolean) ?? [];
  const forbiddenSources = item.forbidden_sources?.map(goldenSourceLabel).filter(Boolean) ?? [];
  const parts = [
    item.answerable === false ? "answerable: no" : item.answerable === true ? "answerable: yes" : "",
    expectedSources.length ? `sources: ${expectedSources.join(", ")}` : "",
    item.required_source_count && item.required_source_count > 1 ? `required sources: ${item.required_source_count}` : "",
    forbiddenSources.length ? `forbidden: ${forbiddenSources.join(", ")}` : "",
    item.expected_document_ids?.length ? `documents: ${item.expected_document_ids.join(", ")}` : "",
    item.expected_chunk_ids?.length ? `chunks: ${item.expected_chunk_ids.join(", ")}` : "",
    item.expected_chunk_contains?.length ? `contains: ${item.expected_chunk_contains.join(", ")}` : ""
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" / ") : "No expectations configured";
}

function goldenSourceLabel(source: NonNullable<RAGEvaluationRunResponse["cases"][number]["expected_sources"]>[number]) {
  return [
    source.document_id ? `document ${source.document_id}` : "",
    source.chunk_id ? `chunk ${source.chunk_id}` : "",
    source.source_uri ?? "",
    source.content_contains?.length ? `contains ${source.content_contains.join(" + ")}` : ""
  ].filter(Boolean).join("; ");
}
