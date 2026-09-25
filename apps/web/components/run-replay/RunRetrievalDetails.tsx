import type { RunEvent } from "../../lib/api";
import type {
  ContextSelectionInfo,
  KnowledgeSecurityInfo,
  RelevanceGateInfo,
  RerankerInfo
} from "../../lib/knowledge-api";

type RetrievedMemoryPayload = {
  id?: string;
  kind?: string;
  content?: string;
  similarity?: number;
  score?: number;
  metadata?: Record<string, unknown>;
};

type RetrievedChunkPayload = {
  source_id?: string;
  document_id?: string;
  document_title?: string;
  document_version?: string;
  chunk_id?: string;
  parent_id?: string;
  section_path?: string[];
  start_offset?: number;
  end_offset?: number;
  content_hash?: string;
  chunk_index?: number;
  content?: string;
  similarity?: number;
  score?: number;
  vector_rank?: number;
  lexical_rank?: number;
  rrf_score?: number;
  fusion_rank?: number;
  rerank_rank?: number;
  rerank_score?: number;
  lexical_boost?: number;
  metadata_boost?: number;
  context_role?: string;
  matched_chunk_id?: string;
  source_chunk_ids?: string[];
  matched_chunk_ids?: string[];
  merged_chunk_count?: number;
  metadata?: Record<string, unknown>;
};

type FusionPayload = {
  algorithm?: string;
  version?: string;
  rank_constant?: number;
  dense_weight?: number;
  lexical_weight?: number;
};

export function RetrievalMetadata({ payload }: { payload: Record<string, unknown> }) {
  const fusion = fusionPayload(payload.fusion);
  const reranker = rerankerPayload(payload.reranker);
  const relevanceGate = relevanceGatePayload(payload.relevance_gate);
  const knowledgeSecurity = knowledgeSecurityPayload(payload.knowledge_security);
  const contextSelection = contextSelectionPayload(payload.context_selection);
  return (
    <>
      {fusion ? (
        <div className="detail-kv">
          <span>Fusion</span>
          <strong>{fusionLabel(fusion)}</strong>
        </div>
      ) : null}
      {reranker ? (
        <div className="detail-kv">
          <span>Reranker</span>
          <strong>{rerankerLabel(reranker)}</strong>
        </div>
      ) : null}
      {relevanceGate ? (
        <div className="detail-kv">
          <span>Relevance Gate</span>
          <strong>{relevanceGateLabel(relevanceGate)}</strong>
        </div>
      ) : null}
      {knowledgeSecurity ? (
        <div className="detail-kv">
          <span>Knowledge security</span>
          <strong>{knowledgeSecurityLabel(knowledgeSecurity)}</strong>
        </div>
      ) : null}
      {contextSelection ? (
        <div className="detail-kv">
          <span>Context selection</span>
          <strong>{contextSelectionLabel(contextSelection)}</strong>
        </div>
      ) : null}
    </>
  );
}

export function RetrievalOverview({ summary }: { summary: ReturnType<typeof buildRetrievalSummary> }) {
  if (summary.eventCount === 0) {
    return (
      <section className="retrieval-overview">
        <div>
          <div className="panel-title inline">Retrieved context</div>
          <p>No retrieval trace events recorded for this run.</p>
        </div>
      </section>
    );
  }
  return (
    <section className="retrieval-overview">
      <div>
        <div className="panel-title inline">Retrieved context</div>
        <p>
          {summary.eventCount} retrieval events, {summary.memoryCount} memories, {summary.matchedChunkCount} matched children, {summary.chunkCount} model-context chunks.
        </p>
      </div>
      <div className="retrieval-model">
        <span>Embedding</span>
        <strong>{summary.embeddingLabel}</strong>
      </div>
      <div className="retrieval-model">
        <span>Executor</span>
        <strong>{summary.executorLabel}</strong>
      </div>
      <div className="retrieval-model">
        <span>Fusion</span>
        <strong>{summary.fusionLabel}</strong>
      </div>
      <div className="retrieval-model">
        <span>Reranker</span>
        <strong>{summary.rerankerLabel}</strong>
      </div>
      <div className="retrieval-model">
        <span>Relevance Gate</span>
        <strong>{summary.relevanceGateLabel}</strong>
      </div>
      <div className="retrieval-model">
        <span>Knowledge security</span>
        <strong>{summary.knowledgeSecurityLabel}</strong>
      </div>
      <div className="retrieval-model">
        <span>Context selection</span>
        <strong>{summary.contextSelectionLabel}</strong>
      </div>
    </section>
  );
}

export function RetrievedContext({ payload }: { payload: Record<string, unknown> }) {
  const memories = retrievedMemories(payload);
  const chunks = retrievedChunks(payload);
  if (memories.length === 0 && chunks.length === 0) return null;
  return (
    <div className="retrieved-context">
      {memories.length > 0 ? (
        <div>
          <div className="retrieved-context-title">Retrieved memories</div>
          <div className="retrieved-list">
            {memories.map((memory, index) => (
              <article className="retrieved-item" key={`${memory.id ?? "memory"}-${index}`}>
                <div className="retrieved-item-header">
                  <strong>{memory.kind || "memory"}</strong>
                  <span>{formatScore(memory.score ?? memory.similarity)}</span>
                </div>
                <p>{memory.content}</p>
                <small>{memory.id}</small>
              </article>
            ))}
          </div>
        </div>
      ) : null}
      {chunks.length > 0 ? (
        <div>
          <div className="retrieved-context-title">Retrieved knowledge chunks</div>
          <div className="retrieved-list">
            {chunks.map((chunk, index) => (
              <article className="retrieved-item" key={`${chunk.chunk_id ?? "chunk"}-${index}`}>
                <div className="retrieved-item-header">
                  <strong>
                    {chunk.document_title || "Untitled document"}
                    {typeof chunk.chunk_index === "number" ? ` #${chunk.chunk_index + 1}` : ""}
                  </strong>
                  <span>{formatScore(chunk.rerank_score ?? chunk.score ?? chunk.similarity)}</span>
                </div>
                <p>{chunk.content}</p>
                <small>
                  {[
					chunk.source_id ? `[${chunk.source_id}]` : "",
                    sourceDetailsLabel(chunk),
                    chunk.context_role ? contextRoleLabel(chunk.context_role) : "",
                    chunk.merged_chunk_count && chunk.merged_chunk_count > 1 ? `${chunk.merged_chunk_count} source chunks` : "",
                    chunk.vector_rank ? `semantic #${chunk.vector_rank}` : "",
                    chunk.lexical_rank ? `keyword #${chunk.lexical_rank}` : "",
                    chunk.fusion_rank ? `fusion #${chunk.fusion_rank}` : "",
                    chunk.rerank_rank ? `rerank #${chunk.rerank_rank}` : "",
                    chunk.rrf_score ? `RRF ${chunk.rrf_score.toFixed(6)}` : "",
                    chunk.lexical_boost ? `lexical +${chunk.lexical_boost.toFixed(3)}` : "",
                  ]
                    .filter(Boolean)
                    .join(" - ")}
                </small>
              </article>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

export function buildRetrievalSummary(events: RunEvent[]) {
	const explicitEvents = events.filter((event) => event.type === "retrieval.completed");
  const retrievalEvents =
    explicitEvents.length > 0
      ? explicitEvents
      : events.filter((event) => retrievedMemories(event.payload).length > 0 || retrievedChunks(event.payload).length > 0);
  const memoryCount = retrievalEvents.reduce((total, event) => total + retrievedMemories(event.payload).length, 0);
  const chunkCount = retrievalEvents.reduce((total, event) => total + retrievedChunks(event.payload).length, 0);
  const matchedChunkCount = retrievalEvents.reduce((total, event) => {
    if ("matched_chunk_count" in event.payload) {
      return total + numberPayload(event.payload, "matched_chunk_count");
    }
    return total + (Array.isArray(event.payload.matched_chunks) ? event.payload.matched_chunks.length : 0);
  }, 0);
  const firstPayload = retrievalEvents[0]?.payload ?? {};
  const provider = stringPayload(firstPayload, "embedding_provider");
  const model = stringPayload(firstPayload, "embedding_model");
  const dimensions = numberPayload(firstPayload, "embedding_dimensions");
  const executor = firstNonEmpty(events.map((event) => stringPayload(event.payload, "executor")));
  const framework = firstNonEmpty(events.map((event) => stringPayload(event.payload, "framework")));
  const fusion = fusionPayload(firstPayload.fusion);
  const reranker = rerankerPayload(firstPayload.reranker);
  const relevanceGate = relevanceGatePayload(firstPayload.relevance_gate);
  const knowledgeSecurity = knowledgeSecurityPayload(firstPayload.knowledge_security);
  const contextSelection = contextSelectionPayload(firstPayload.context_selection);
  return {
    eventCount: retrievalEvents.length,
    memoryCount,
    chunkCount,
    matchedChunkCount,
    embeddingLabel: provider || model ? [provider, model, dimensions ? `${dimensions}d` : ""].filter(Boolean).join(" / ") : "not recorded",
    executorLabel: executor || framework ? [executor, framework].filter(Boolean).join(" / ") : "not recorded",
    fusionLabel: fusion ? fusionLabel(fusion) : "not recorded",
    rerankerLabel: reranker ? rerankerLabel(reranker) : "not recorded",
    relevanceGateLabel: relevanceGate ? relevanceGateLabel(relevanceGate) : "not recorded",
    knowledgeSecurityLabel: knowledgeSecurity ? knowledgeSecurityLabel(knowledgeSecurity) : "not recorded",
    contextSelectionLabel: contextSelection ? contextSelectionLabel(contextSelection) : "not recorded"
  };
}

function contextSelectionPayload(value: unknown): ContextSelectionInfo | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as ContextSelectionInfo;
}

function contextSelectionLabel(selection: ContextSelectionInfo) {
  const transformation = selection.transformation;
  const transformationLabel = transformation
    ? ` / context ${transformation.input_chunks} to ${transformation.output_chunks} / ${transformation.duplicates_removed} duplicates / ${transformation.adjacent_merges} merges`
    : "";
  return `${selection.version} / ${selection.tokens_used.toLocaleString()} of ${selection.max_tokens.toLocaleString()} tokens / ${selection.parent_chunks} parent / ${selection.adjacent_chunks} adjacent${transformationLabel}`;
}

function contextRoleLabel(role: string) {
  switch (role) {
    case "parent":
      return "parent context";
    case "adjacent":
      return "adjacent context";
    default:
      return "matched child";
  }
}

function knowledgeSecurityPayload(value: unknown): KnowledgeSecurityInfo | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as KnowledgeSecurityInfo;
}

function knowledgeSecurityLabel(security: KnowledgeSecurityInfo) {
  const reasons = Array.from(new Set((security.decisions ?? []).flatMap((decision) => decision.reasons ?? [])));
  const reasonLabel = reasons.length > 0 ? ` / ${reasons.join(", ")}` : "";
  return `${security.policy_version} / checked ${security.checked_candidates} / blocked ${security.blocked_candidates}${reasonLabel}`;
}

function fusionPayload(value: unknown): FusionPayload | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as FusionPayload;
}

function fusionLabel(fusion: FusionPayload) {
  const algorithm = fusion.algorithm?.toUpperCase() || "RRF";
  const version = fusion.version || "unversioned";
  const rankConstant = typeof fusion.rank_constant === "number" ? `k=${fusion.rank_constant}` : "k=?";
  const weights =
    typeof fusion.dense_weight === "number" && typeof fusion.lexical_weight === "number"
      ? `semantic ${fusion.dense_weight.toFixed(1)} / keyword ${fusion.lexical_weight.toFixed(1)}`
      : "weights not recorded";
  return `${algorithm} / ${version} / ${rankConstant} / ${weights}`;
}

function rerankerPayload(value: unknown): RerankerInfo | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as RerankerInfo;
}

function rerankerLabel(reranker: RerankerInfo) {
  const identity = [
    reranker.algorithm || "unknown",
    reranker.version || "unversioned",
    `config ${reranker.config_version || "unversioned"}`
  ];
  const provider = [reranker.provider, reranker.model].filter(Boolean).join(" / ");
  if (provider) {
    identity.push(provider);
  }
  return identity.join(" / ");
}

function relevanceGatePayload(value: unknown): RelevanceGateInfo | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return null;
  }
  return value as RelevanceGateInfo;
}

function relevanceGateLabel(gate: RelevanceGateInfo) {
  const threshold = Number.isFinite(gate.minimum_evidence_coverage)
    ? ` / evidence coverage ≥ ${gate.minimum_evidence_coverage.toFixed(2)}`
    : "";
  return `${gate.policy || "unknown"} / ${gate.version || "unversioned"} / config ${gate.config_version || "unversioned"}${threshold}`;
}

function retrievedMemories(payload: Record<string, unknown>): RetrievedMemoryPayload[] {
  return arrayPayload<RetrievedMemoryPayload>(payload.retrieved_memories);
}

function retrievedChunks(payload: Record<string, unknown>): RetrievedChunkPayload[] {
  return arrayPayload<RetrievedChunkPayload>(payload.retrieved_chunks);
}

function arrayPayload<T>(value: unknown): T[] {
  return Array.isArray(value) ? (value as T[]) : [];
}

export function stringPayload(payload: Record<string, unknown>, key: string) {
  const value = payload[key];
  return typeof value === "string" ? value : "";
}

function numberPayload(payload: Record<string, unknown>, key: string) {
  const value = payload[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function firstNonEmpty(values: string[]) {
  return values.find((value) => value.trim()) ?? "";
}

function formatScore(value: unknown) {
  const numberValue = typeof value === "number" ? value : Number(value ?? 0);
  return Number.isFinite(numberValue) ? numberValue.toFixed(3) : "n/a";
}

function sourceDetailsLabel(chunk: RetrievedChunkPayload) {
  const details = [
    chunk.source_chunk_ids?.length ? `chunks ${chunk.source_chunk_ids.join(", ")}` : chunk.chunk_id,
    chunk.section_path?.join(" > "),
    sourceRangeLabel(chunk.start_offset, chunk.end_offset),
    shortSourceLabel("version", chunk.document_version),
    shortSourceLabel("hash", chunk.content_hash)
  ].filter(Boolean);
  return details.length > 0 ? `Source details: ${details.join(" / ")}` : "";
}

function sourceRangeLabel(start: number | undefined, end: number | undefined) {
  return typeof start === "number" && typeof end === "number" && end > start ? `bytes ${start}-${end}` : "";
}

function shortSourceLabel(label: string, value: string | undefined) {
  if (!value) return "";
  const normalized = value.startsWith("sha256:") ? value.slice(7) : value;
  return `${label} ${normalized.slice(0, 12)}`;
}
