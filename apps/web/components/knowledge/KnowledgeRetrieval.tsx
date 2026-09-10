import type {
  ContextSelectionInfo,
  EmbeddingInfo,
  FusionInfo,
  KnowledgeSecurityInfo,
  RerankerInfo,
  RelevanceGateInfo,
  RetrievedDocumentChunk
} from "../../lib/knowledge-api";
import { documentFilename, documentFormat, metadataString, shortSourceLabel } from "./knowledgeFormatters";

export function KnowledgeResultCard({ result }: { result: RetrievedDocumentChunk }) {
  return (
    <article className="rag-result-card">
      <div className="rag-result-header">
        <div>
          <h3>{result.document.title}</h3>
          <div className="tool-source">Source details: {chunkSourceDetails(result)}</div>
        </div>
        <div className="document-metrics">
          <span>{documentFormat(result.document)}</span>
          <span>Semantic #{result.vector_rank ?? "-"}</span>
          <span>Keyword #{result.lexical_rank ?? "-"}</span>
          <span>Fusion #{result.fusion_rank ?? "-"}</span>
          <span>Final #{result.rerank_rank ?? "-"}</span>
          {result.confidence ? <span>{result.confidence}</span> : null}
          <span>similarity {formatScore(result.similarity)}</span>
          <span>final {formatScore(result.rerank_score ?? result.score)}</span>
        </div>
      </div>
      <ScoreBreakdown result={result} />
      <p>{result.chunk.content}</p>
    </article>
  );
}

export function RetrievalDiagnostics({
  contextSelection,
  embedding,
  fusion,
  gate,
  hasSearched,
  reranker,
  security
}: {
  contextSelection?: ContextSelectionInfo | null;
  embedding: EmbeddingInfo | null;
  fusion: FusionInfo | null;
  gate: RelevanceGateInfo | null;
  hasSearched: boolean;
  reranker: RerankerInfo | null;
  security?: KnowledgeSecurityInfo | null;
}) {
  if (!hasSearched) return null;
  return (
    <details className="retrieval-diagnostics">
      <summary>Retrieval diagnostics</summary>
      <div className="retrieval-diagnostics-body">
        <EmbeddingStatus embedding={embedding} />
        <FusionStatus fusion={fusion} />
        <RerankerStatus reranker={reranker} />
        <RelevanceGateStatus gate={gate} />
        {security !== undefined ? <KnowledgeSecurityStatus security={security} /> : null}
        {contextSelection !== undefined ? <ContextSelectionStatus selection={contextSelection} /> : null}
      </div>
    </details>
  );
}

export function ModelContextPreview({
  items,
  selection
}: {
  items: RetrievedDocumentChunk[];
  selection: ContextSelectionInfo | null;
}) {
  if (!selection || items.length === 0) return null;
  return (
    <section className="rag-context-preview">
      <div className="knowledge-section-heading">
        <h2>Model context</h2>
        <span>{items.length} chunks after expansion and token selection</span>
      </div>
      <div className="rag-results compact-results">
        {items.map((item) => (
          <article className="rag-result-card context-result-card" key={item.chunk.id}>
            <div className="rag-result-header">
              <div>
                <h3>{item.document.title}</h3>
                <div className="tool-source">Source details: {chunkSourceDetails(item)}</div>
              </div>
              <div className="document-metrics">
                {item.source_id ? <span>[{item.source_id}]</span> : null}
                <span>{contextRoleLabel(item.context_role)}</span>
                {item.merged_chunk_count && item.merged_chunk_count > 1 ? <span>{item.merged_chunk_count} source chunks</span> : null}
                <span>{item.chunk.token_count} tokens</span>
              </div>
            </div>
            <p>{item.chunk.content}</p>
          </article>
        ))}
      </div>
    </section>
  );
}

function ScoreBreakdown({ result }: { result: RetrievedDocumentChunk }) {
  const terms = result.matched_terms ?? [];
  return (
    <div className="score-breakdown">
      <span>recall {formatScore(result.score)}</span>
      {result.rrf_score !== undefined ? <span>RRF {formatRRFScore(result.rrf_score)}</span> : null}
      <span>evidence {formatScore(result.evidence_score ?? 0)}</span>
      <span>coverage {formatPercent(result.evidence_coverage ?? 0)}</span>
      <span>lexical +{formatScore(result.lexical_boost ?? 0)}</span>
      <span>metadata +{formatScore(result.metadata_boost ?? 0)}</span>
      {result.diversity_penalty ? <span>diversity -{formatScore(result.diversity_penalty)}</span> : null}
      {result.confidence ? <span>confidence {result.confidence}</span> : null}
      <span>{terms.length > 0 ? `matched ${terms.join(", ")}` : "matched none"}</span>
      {result.filter_reason ? <span>{result.filter_reason}</span> : null}
    </div>
  );
}

function EmbeddingStatus({ embedding }: { embedding: EmbeddingInfo | null }) {
  if (!embedding) return <DiagnosticRow warning label="Embedding" value="Metadata unavailable" />;
  return (
    <DiagnosticRow
      label="Embedding"
      value={`${embedding.provider} / ${embedding.model}${embedding.dimensions ? ` / ${embedding.dimensions}d` : ""}${embedding.estimated ? " / local fallback" : ""}`}
      warning={Boolean(embedding.estimated)}
    />
  );
}

function FusionStatus({ fusion }: { fusion: FusionInfo | null }) {
  if (!fusion) return <DiagnosticRow warning label="Fusion" value="Metadata unavailable" />;
  return <DiagnosticRow label="Fusion" value={`${fusion.algorithm.toUpperCase()} / ${fusion.version} / k=${fusion.rank_constant} / semantic ${fusion.dense_weight.toFixed(1)} / keyword ${fusion.lexical_weight.toFixed(1)}`} />;
}

function RerankerStatus({ reranker }: { reranker: RerankerInfo | null }) {
  if (!reranker) return <DiagnosticRow warning label="Reranker" value="Metadata unavailable" />;
  const provider = [reranker.provider, reranker.model].filter(Boolean).join(" / ");
  return <DiagnosticRow label="Reranker" value={[reranker.algorithm, reranker.version, `config ${reranker.config_version}`, provider].filter(Boolean).join(" / ")} />;
}

function RelevanceGateStatus({ gate }: { gate: RelevanceGateInfo | null }) {
  if (!gate) return <DiagnosticRow warning label="Relevance gate" value="Metadata unavailable" />;
  const coverage = Number.isFinite(gate.minimum_evidence_coverage) ? ` / evidence coverage ≥ ${gate.minimum_evidence_coverage.toFixed(2)}` : "";
  return <DiagnosticRow label="Relevance gate" value={`${gate.policy} / ${gate.version} / config ${gate.config_version}${coverage}`} />;
}

function KnowledgeSecurityStatus({ security }: { security: KnowledgeSecurityInfo | null }) {
  if (!security) return <DiagnosticRow warning label="Knowledge security" value="Metadata unavailable" />;
  const reasons = Array.from(new Set((security.decisions ?? []).flatMap((decision) => decision.reasons ?? [])));
  const suffix = reasons.length > 0 ? ` / ${reasons.join(", ")}` : "";
  return <DiagnosticRow label="Knowledge security" value={`${security.policy_version} / checked ${security.checked_candidates} / blocked ${security.blocked_candidates}${suffix}`} warning={security.blocked_candidates > 0} />;
}

function ContextSelectionStatus({ selection }: { selection: ContextSelectionInfo | null }) {
  if (!selection) return <DiagnosticRow warning label="Context selection" value="Metadata unavailable" />;
  const transformation = selection.transformation
    ? ` / ${selection.transformation.input_chunks} to ${selection.transformation.output_chunks} chunks / ${selection.transformation.duplicates_removed} duplicates removed / ${selection.transformation.adjacent_merges} merges`
    : "";
  return <DiagnosticRow label="Context selection" value={`${selection.version} / ${selection.tokens_used.toLocaleString()} of ${selection.max_tokens.toLocaleString()} tokens / ${selection.matched_children} matched / ${selection.parent_chunks} parent / ${selection.adjacent_chunks} adjacent${transformation} / ${selection.scope_filtered ? "scope filtered" : "scope unavailable"}`} />;
}

function DiagnosticRow({ label, value, warning = false }: { label: string; value: string; warning?: boolean }) {
  return (
    <div className={warning ? "retrieval-diagnostic warning" : "retrieval-diagnostic"}>
      <strong>{label}</strong>
      <span>{value}</span>
    </div>
  );
}

function contextRoleLabel(role: RetrievedDocumentChunk["context_role"]) {
  if (role === "parent") return "Parent context";
  if (role === "adjacent") return "Adjacent context";
  return "Matched child";
}

function chunkSourceDetails(result: RetrievedDocumentChunk) {
  const isMerged = Boolean(result.merged_chunk_count && result.merged_chunk_count > 1);
  const parts = [
    documentFilename(result.document),
    result.source_chunk_ids?.length ? `chunks ${result.source_chunk_ids.join(", ")}` : "",
    result.chunk.section_path?.join(" > ") || (isMerged ? "" : metadataString(result.chunk.metadata, "heading_path")),
    isMerged ? "merged context" : metadataString(result.chunk.metadata, "chunk_type"),
    sourceRangeLabel(result.chunk.start_offset, result.chunk.end_offset),
    shortSourceLabel("version", result.chunk.document_version),
    shortSourceLabel("hash", result.chunk.content_hash)
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" / ") : `Chunk ${result.chunk.chunk_index + 1}`;
}

function sourceRangeLabel(start: number | undefined, end: number | undefined) {
  return typeof start === "number" && typeof end === "number" && end > start ? `bytes ${start}-${end}` : "";
}

export function formatScore(value: number) {
  return Number.isFinite(value) ? value.toFixed(3) : "0.000";
}

function formatRRFScore(value: number) {
  return Number.isFinite(value) ? value.toFixed(6) : "0.000000";
}

export function formatPercent(value: number) {
  return Number.isFinite(value) ? `${Math.round(value * 100)}%` : "0%";
}
