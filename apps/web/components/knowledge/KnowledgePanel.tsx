"use client";

import type {
  ContextSelectionInfo,
  DocumentDetail,
  DocumentInfo,
  EmbeddingInfo,
  FusionInfo,
  KnowledgeSecurityInfo,
  RerankerInfo,
  RelevanceGateInfo,
  RetrievedDocumentChunk
} from "../../lib/knowledge-api";
import type { KnowledgeWorkbenchModel } from "./useKnowledgeWorkbench";

export function KnowledgePanel({ model }: { model: KnowledgeWorkbenchModel }) {
  const {
    contextItems,
    contextSelection,
    knowledgeContextMaxTokens,
    documents,
    documentTitle,
    documentContent,
    deletingDocumentId,
    error,
    hasSearched,
    isCreating,
    isLoadingDocumentDetail,
    isSearching,
    isUploading,
    minSimilarity,
    noMatchReason,
    query,
    results,
    searchEmbedding,
    searchFusion,
    searchReranker,
    searchRelevanceGate,
    searchSecurity,
    selectedDocument,
    selectedDocumentId,
    uploadFile,
    uploadTitle
  } = model;

  return (
    <section className="knowledge-panel">
      {error ? <div className="error">{error}</div> : null}
      <div className="knowledge-grid">
        <section className="knowledge-column">
          <div className="panel-title">Upload knowledge file</div>
          <div className="knowledge-form upload-form">
            <input
              value={uploadTitle}
              onChange={(event) => model.setUploadTitle(event.target.value)}
              placeholder="Optional document title"
            />
            <label className="upload-file-control">
              <input
                accept=".txt,.md,.markdown,text/plain,text/markdown"
                onChange={model.selectUploadFile}
                type="file"
              />
              <span className="upload-file-action">Choose File</span>
              <span className={`upload-file-name ${uploadFile ? "" : "empty"}`}>
                {uploadFile ? `${uploadFile.name} (${formatBytes(uploadFile.size)})` : "No .txt or .md file selected"}
              </span>
            </label>
            <button
              className="send"
              disabled={isUploading || !uploadFile}
              onClick={model.uploadKnowledgeDocument}
              type="button"
            >
              {isUploading ? "Uploading..." : "Upload File"}
            </button>
          </div>
          <div className="panel-title secondary-panel-title">Add text document</div>
          <div className="knowledge-form">
            <input
              value={documentTitle}
              onChange={(event) => model.setDocumentTitle(event.target.value)}
              placeholder="Document title"
            />
            <textarea
              value={documentContent}
              onChange={(event) => model.setDocumentContent(event.target.value)}
              placeholder="Paste text knowledge here..."
            />
            <button
              className="send"
              disabled={isCreating || documentTitle.trim().length === 0 || documentContent.trim().length === 0}
              onClick={model.createTextDocument}
              type="button"
            >
              {isCreating ? "Adding..." : "Add Document"}
            </button>
          </div>
        </section>

        <section className="knowledge-column">
          <div className="knowledge-header-row">
            <div className="panel-title">Documents</div>
            <button className="secondary-action" onClick={model.refreshDocuments} type="button">
              Refresh
            </button>
          </div>
          <div className="document-list">
            {documents.length === 0 ? (
              <div className="empty compact">No documents indexed yet.</div>
            ) : (
              documents.map((document) => {
                const isSelected = selectedDocumentId === document.id;
                const detail = selectedDocument?.document.id === document.id ? selectedDocument : null;

                return (
                  <article className="document-card" key={document.id}>
                    <div className="document-card-summary">
                      <div>
                        <h3>{document.title}</h3>
                        <div className="tool-source">
                          {documentFilename(document) || new Date(document.created_at).toLocaleString()}
                        </div>
                      </div>
                      <div className="document-metrics">
                        <span>{documentFormat(document)}</span>
                        <span>{document.chunk_count ?? 0} chunks</span>
                        <span>{document.embedding_count ?? 0} embeddings</span>
                      </div>
                      <div className="document-actions">
                        <button
                          className="secondary-action"
                          onClick={() => model.selectDocument(document.id)}
                          type="button"
                        >
                          Details
                        </button>
                        <button
                          className="secondary-action danger-action"
                          disabled={deletingDocumentId === document.id}
                          onClick={() => model.removeDocument(document)}
                          type="button"
                        >
                          {deletingDocumentId === document.id ? "Deleting..." : "Delete"}
                        </button>
                      </div>
                    </div>
                    {isSelected ? (
                      <DocumentDetailBlock
                        detail={detail}
                        isLoading={isLoadingDocumentDetail && selectedDocumentId === document.id}
                      />
                    ) : null}
                  </article>
                );
              })
            )}
          </div>
        </section>
      </div>

      <section className="knowledge-search">
        <div className="panel-title">Search indexed chunks</div>
        <div className="knowledge-search-row">
          <input
            value={query}
            onChange={(event) => model.setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                void model.searchKnowledge();
              }
            }}
            placeholder="Ask a semantic question..."
          />
          <label className="threshold-input">
            <span>Min similarity</span>
            <input
              max="1"
              min="0"
              onChange={(event) => model.setMinSimilarity(event.target.value)}
              step="0.05"
              type="number"
              value={minSimilarity}
            />
          </label>
          <label className="threshold-input">
            <span>Knowledge context limit</span>
            <input
              min="1"
              onChange={(event) => model.setKnowledgeContextMaxTokens(event.target.value)}
              step="100"
              type="number"
              value={knowledgeContextMaxTokens}
            />
          </label>
          <button
            className="send"
            disabled={isSearching || query.trim().length === 0}
            onClick={model.searchKnowledge}
            type="button"
          >
            {isSearching ? "Searching..." : "Search"}
          </button>
        </div>
        <EmbeddingStatus embedding={searchEmbedding} hasSearched={hasSearched} />
        <FusionStatus fusion={searchFusion} hasSearched={hasSearched} />
        <RerankerStatus hasSearched={hasSearched} reranker={searchReranker} />
        <RelevanceGateStatus gate={searchRelevanceGate} hasSearched={hasSearched} />
        <KnowledgeSecurityStatus hasSearched={hasSearched} security={searchSecurity} />
        <ContextSelectionStatus hasSearched={hasSearched} selection={contextSelection} />
        {hasSearched && noMatchReason ? <div className="rag-no-match">{noMatchReason}</div> : null}
        <div className="rag-results">
          {results.length === 0 ? (
            <div className="empty compact">
              {hasSearched && noMatchReason
                ? "No results passed the relevance gate."
                : "Search results will appear here."}
            </div>
          ) : (
            results.map((result) => (
              <article className="rag-result-card" key={result.chunk.id}>
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
            ))
          )}
        </div>
        <ModelContextPreview items={contextItems} selection={contextSelection} />
      </section>

    </section>
  );
}

function ContextSelectionStatus({
  selection,
  hasSearched
}: {
  selection: ContextSelectionInfo | null;
  hasSearched: boolean;
}) {
  if (!hasSearched) {
    return null;
  }
  if (!selection) {
    return (
      <div className="embedding-status warning">
        <span>Context selection: metadata unavailable</span>
      </div>
    );
  }
  return (
    <div className="embedding-status">
      <span>
        Context: {selection.version} / {selection.tokens_used.toLocaleString()} of {selection.max_tokens.toLocaleString()} tokens
      </span>
      <span>
        {selection.matched_children} matched / {selection.parent_chunks} parent / {selection.adjacent_chunks} adjacent
      </span>
      {selection.transformation ? (
        <span>
          Context merge: {selection.transformation.input_chunks} to {selection.transformation.output_chunks} /{" "}
          {selection.transformation.duplicates_removed} duplicates / {selection.transformation.adjacent_merges} merges /{" "}
          {selection.transformation.document_groups} documents
        </span>
      ) : null}
      <span>{selection.scope_filtered ? "Scope filtered" : "Scope unavailable"}</span>
    </div>
  );
}

function ModelContextPreview({
  items,
  selection
}: {
  items: RetrievedDocumentChunk[];
  selection: ContextSelectionInfo | null;
}) {
  if (!selection || items.length === 0) {
    return null;
  }
  return (
    <section className="rag-context-preview">
      <div className="knowledge-header-row">
        <div className="panel-title">Model context</div>
        <div className="tool-source">{items.length} chunks after expansion and limit selection</div>
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
                {item.merged_chunk_count && item.merged_chunk_count > 1 ? (
                  <span>{item.merged_chunk_count} source chunks</span>
                ) : null}
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

function contextRoleLabel(role: RetrievedDocumentChunk["context_role"]) {
  switch (role) {
    case "parent":
      return "Parent context";
    case "adjacent":
      return "Adjacent context";
    default:
      return "Matched child";
  }
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
      {terms.length > 0 ? <span>matched {terms.join(", ")}</span> : <span>matched none</span>}
      {result.filter_reason ? <span>{result.filter_reason}</span> : null}
    </div>
  );
}

function DocumentDetailBlock({ detail, isLoading }: { detail: DocumentDetail | null; isLoading: boolean }) {
  if (isLoading) {
    return <div className="document-detail-inline empty compact">Loading document...</div>;
  }
  if (!detail) {
    return <div className="document-detail-inline empty compact">Document detail unavailable.</div>;
  }

  return (
    <div className="document-detail-inline">
      <div className="document-detail-header">
        <div>
          <h3>Document detail</h3>
          <div className="tool-source">
            {[
              documentFilename(detail.document),
              shortSourceLabel("version", detail.document.version),
              new Date(detail.document.created_at).toLocaleString()
            ]
              .filter(Boolean)
              .join(" / ")}
          </div>
        </div>
        <div className="document-metrics">
          <span>{documentFormat(detail.document)}</span>
          <span>{detail.document.chunk_count ?? detail.chunks.length} chunks</span>
          <span>{detail.document.embedding_count ?? 0} embeddings</span>
        </div>
      </div>
      <div className="chunk-list">
        {detail.chunks.map((chunk) => (
          <article className="chunk-card" key={chunk.id}>
            <div className="rag-result-header">
              <div>
                <h3>Chunk {chunk.chunk_index + 1}</h3>
                <div className="tool-source">Source details: {documentChunkSourceDetails(chunk)}</div>
              </div>
              <div className="document-metrics">
                {metadataString(chunk.metadata, "chunk_type") ? (
                  <span>{metadataString(chunk.metadata, "chunk_type")}</span>
                ) : null}
                <span>{chunk.token_count} tokens</span>
              </div>
            </div>
            <p>{chunk.content}</p>
          </article>
        ))}
      </div>
    </div>
  );
}

function EmbeddingStatus({ embedding, hasSearched }: { embedding: EmbeddingInfo | null; hasSearched: boolean }) {
  if (!hasSearched) {
    return (
      <div className="embedding-status">
        <span>Embedding: not checked yet</span>
        <span>Run a search to show provider/model.</span>
      </div>
    );
  }
  if (!embedding) {
    return (
      <div className="embedding-status warning">
        <span>Embedding: metadata unavailable</span>
        <span>The API response did not include provider/model; restart the backend if it is still running old code.</span>
      </div>
    );
  }

  return (
    <div className={`embedding-status ${embedding.estimated ? "warning" : ""}`}>
      <span>
        Embedding: {embedding.provider} / {embedding.model}
        {embedding.dimensions ? ` / ${embedding.dimensions}d` : ""}
      </span>
      {embedding.estimated ? (
        <span>Local fallback is active; semantic quality is limited.</span>
      ) : (
        <span>Semantic vector search is using the configured embedding provider.</span>
      )}
    </div>
  );
}

function FusionStatus({ fusion, hasSearched }: { fusion: FusionInfo | null; hasSearched: boolean }) {
  if (!hasSearched) {
    return null;
  }
  if (!fusion) {
    return (
      <div className="embedding-status warning">
        <span>Fusion: metadata unavailable</span>
      </div>
    );
  }
  return (
    <div className="embedding-status">
      <span>
        Fusion: {fusion.algorithm.toUpperCase()} / {fusion.version} / k={fusion.rank_constant}
      </span>
      <span>
        Semantic {fusion.dense_weight.toFixed(1)} / Keyword {fusion.lexical_weight.toFixed(1)}
      </span>
    </div>
  );
}

function RerankerStatus({ reranker, hasSearched }: { reranker: RerankerInfo | null; hasSearched: boolean }) {
  if (!hasSearched) {
    return null;
  }
  if (!reranker) {
    return (
      <div className="embedding-status warning">
        <span>Reranker: metadata unavailable</span>
      </div>
    );
  }
  const provider = [reranker.provider, reranker.model].filter(Boolean).join(" / ");
  return (
    <div className="embedding-status">
      <span>
        Reranker: {reranker.algorithm} / {reranker.version}
      </span>
      <span>{[`Config ${reranker.config_version}`, provider].filter(Boolean).join(" / ")}</span>
    </div>
  );
}

function RelevanceGateStatus({ gate, hasSearched }: { gate: RelevanceGateInfo | null; hasSearched: boolean }) {
  if (!hasSearched) {
    return null;
  }
  if (!gate) {
    return (
      <div className="embedding-status warning">
        <span>Relevance Gate: metadata unavailable</span>
      </div>
    );
  }
  return (
    <div className="embedding-status">
      <span>
        Relevance Gate: {gate.policy} / {gate.version}
      </span>
      <span>Config {gate.config_version}</span>
    </div>
  );
}

function KnowledgeSecurityStatus({
  security,
  hasSearched
}: {
  security: KnowledgeSecurityInfo | null;
  hasSearched: boolean;
}) {
  if (!hasSearched) {
    return null;
  }
  if (!security) {
    return (
      <div className="embedding-status warning">
        <span>Knowledge security: metadata unavailable</span>
      </div>
    );
  }
  return (
    <div className={`embedding-status ${security.blocked_candidates > 0 ? "warning" : ""}`}>
      <span>{knowledgeSecurityLabel(security)}</span>
      {security.blocked_candidates > 0 ? <span>{knowledgeSecurityReasons(security)}</span> : null}
    </div>
  );
}

function knowledgeSecurityLabel(security: KnowledgeSecurityInfo) {
  return `Knowledge security: ${security.policy_version} / checked ${security.checked_candidates} / blocked ${security.blocked_candidates}`;
}

function knowledgeSecurityReasons(security: KnowledgeSecurityInfo) {
  const reasons = Array.from(new Set((security.decisions ?? []).flatMap((decision) => decision.reasons ?? [])));
  return reasons.length > 0 ? `Reasons: ${reasons.join(", ")}` : "Reasons not recorded";
}

function formatScore(value: number) {
  return Number.isFinite(value) ? value.toFixed(3) : "0.000";
}

function formatRRFScore(value: number) {
  return Number.isFinite(value) ? value.toFixed(6) : "0.000000";
}

function formatPercent(value: number) {
  return Number.isFinite(value) ? `${Math.round(value * 100)}%` : "0%";
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return "0 B";
  }
  if (value < 1024) {
    return `${value} B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(1)} KB`;
  }
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

function metadataString(metadata: Record<string, unknown> | undefined, key: string) {
  const value = metadata?.[key];
  return typeof value === "string" ? value : "";
}

function documentFormat(document: DocumentInfo) {
  return metadataString(document.metadata, "format") || document.source_type || "text";
}

function documentFilename(document: DocumentInfo) {
  return metadataString(document.metadata, "filename") || document.source_uri || "";
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

function documentChunkSourceDetails(chunk: RetrievedDocumentChunk["chunk"]) {
  return [
    chunk.section_path?.join(" > ") || metadataString(chunk.metadata, "heading_path") || "document root",
    sourceRangeLabel(chunk.start_offset, chunk.end_offset),
    shortSourceLabel("version", chunk.document_version),
    shortSourceLabel("hash", chunk.content_hash)
  ]
    .filter(Boolean)
    .join(" / ");
}

function sourceRangeLabel(start: number | undefined, end: number | undefined) {
  return typeof start === "number" && typeof end === "number" && end > start ? `bytes ${start}-${end}` : "";
}

function shortSourceLabel(label: string, value: string | undefined) {
  if (!value) return "";
  const normalized = value.startsWith("sha256:") ? value.slice(7) : value;
  return `${label} ${normalized.slice(0, 12)}`;
}
