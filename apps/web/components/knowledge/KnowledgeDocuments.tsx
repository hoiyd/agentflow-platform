"use client";

import { useState } from "react";

import type { DocumentDetail, RetrievedDocumentChunk } from "../../lib/knowledge-api";
import type { KnowledgeWorkbenchModel } from "./useKnowledgeWorkbench";
import { documentFilename, documentFormat, metadataString, shortSourceLabel } from "./knowledgeFormatters";

type IngestMode = "file" | "text";

export function KnowledgeDocuments({ model }: { model: KnowledgeWorkbenchModel }) {
  const [ingestMode, setIngestMode] = useState<IngestMode>("file");

  return (
    <div className="knowledge-grid">
      <section className="knowledge-column knowledge-ingest">
        <div className="knowledge-section-heading"><h2>Add document</h2></div>
        <div aria-label="Document input method" className="knowledge-input-tabs" role="tablist">
          <button aria-controls="knowledge-ingest-form" aria-selected={ingestMode === "file"} id="knowledge-ingest-file" onClick={() => setIngestMode("file")} role="tab" type="button">
            Upload file
          </button>
          <button aria-controls="knowledge-ingest-form" aria-selected={ingestMode === "text"} id="knowledge-ingest-text" onClick={() => setIngestMode("text")} role="tab" type="button">
            Paste text
          </button>
        </div>

        {ingestMode === "file" ? (
          <div aria-labelledby="knowledge-ingest-file" className="knowledge-form" id="knowledge-ingest-form" role="tabpanel">
            <input
              aria-label="Optional document title"
              value={model.uploadTitle}
              onChange={(event) => model.setUploadTitle(event.target.value)}
              placeholder="Optional document title"
            />
            <label className="upload-file-control">
              <input accept=".txt,.md,.markdown,text/plain,text/markdown" onChange={model.selectUploadFile} type="file" />
              <span className="upload-file-action">Choose file</span>
              <span className={model.uploadFile ? "upload-file-name" : "upload-file-name empty"}>
                {model.uploadFile
                  ? `${model.uploadFile.name} (${formatBytes(model.uploadFile.size)})`
                  : "No .txt or .md file selected"}
              </span>
            </label>
            <button
              className="send"
              disabled={model.isUploading || !model.uploadFile}
              onClick={model.uploadKnowledgeDocument}
              type="button"
            >
              {model.isUploading ? "Uploading..." : "Upload file"}
            </button>
          </div>
        ) : (
          <div aria-labelledby="knowledge-ingest-text" className="knowledge-form" id="knowledge-ingest-form" role="tabpanel">
            <input
              aria-label="Document title"
              value={model.documentTitle}
              onChange={(event) => model.setDocumentTitle(event.target.value)}
              placeholder="Document title"
            />
            <textarea
              aria-label="Document content"
              value={model.documentContent}
              onChange={(event) => model.setDocumentContent(event.target.value)}
              placeholder="Paste text knowledge"
            />
            <button
              className="send"
              disabled={model.isCreating || model.documentTitle.trim().length === 0 || model.documentContent.trim().length === 0}
              onClick={model.createTextDocument}
              type="button"
            >
              {model.isCreating ? "Adding..." : "Add document"}
            </button>
          </div>
        )}
      </section>

      <section className="knowledge-column knowledge-library">
        <div className="knowledge-section-heading">
          <h2>Indexed documents</h2>
          <button className="secondary-action" onClick={model.refreshDocuments} type="button">Refresh</button>
        </div>
        <div className="document-list">
          {model.documents.length === 0 ? (
            <div className="knowledge-empty">No documents indexed yet.</div>
          ) : (
            model.documents.map((document) => {
              const isSelected = model.selectedDocumentId === document.id;
              const detail = model.selectedDocument?.document.id === document.id ? model.selectedDocument : null;
              return (
                <article className="document-card" key={document.id}>
                  <div className="document-card-summary">
                    <div className="document-title">
                      <h3>{document.title}</h3>
                      <div className="tool-source">{documentFilename(document) || new Date(document.created_at).toLocaleString()}</div>
                    </div>
                    <div className="document-metrics">
                      <span>{documentFormat(document)}</span>
                      <span>{document.chunk_count ?? 0} chunks</span>
                      <span>{document.embedding_count ?? 0} embeddings</span>
                    </div>
                    <div className="document-actions">
                      <button className="secondary-action" onClick={() => model.selectDocument(document.id)} type="button">
                        {isSelected ? "Reload details" : "Details"}
                      </button>
                      <button
                        className="secondary-action danger-action"
                        disabled={model.deletingDocumentId === document.id}
                        onClick={() => model.removeDocument(document)}
                        type="button"
                      >
                        {model.deletingDocumentId === document.id ? "Deleting..." : "Delete"}
                      </button>
                    </div>
                  </div>
                  {isSelected ? (
                    <DocumentDetailBlock
                      detail={detail}
                      isLoading={model.isLoadingDocumentDetail && model.selectedDocumentId === document.id}
                    />
                  ) : null}
                </article>
              );
            })
          )}
        </div>
      </section>
    </div>
  );
}

function DocumentDetailBlock({ detail, isLoading }: { detail: DocumentDetail | null; isLoading: boolean }) {
  if (isLoading) return <div className="document-detail-inline knowledge-empty">Loading document...</div>;
  if (!detail) return <div className="document-detail-inline knowledge-empty">Document detail unavailable.</div>;

  return (
    <div className="document-detail-inline">
      <div className="document-detail-header">
        <div>
          <h3>Document detail</h3>
          <div className="tool-source">
            {[documentFilename(detail.document), shortSourceLabel("version", detail.document.version), new Date(detail.document.created_at).toLocaleString()]
              .filter(Boolean)
              .join(" / ")}
          </div>
        </div>
        <div className="document-metrics">
          <span>{documentFormat(detail.document)}</span>
          <span>{detail.document.chunk_count ?? detail.chunks.length} chunks</span>
          <span>{detail.document.embedding_count ?? 0} embeddings</span>
          {detail.document.index_identity?.chunker_version ? <span>{detail.document.index_identity.chunker_version}</span> : null}
          {detail.document.index_identity?.embedding_model ? (
            <span>{detail.document.index_identity.embedding_provider}/{detail.document.index_identity.embedding_model} · {detail.document.index_identity.embedding_dimensions}d</span>
          ) : null}
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
                {metadataString(chunk.metadata, "chunk_type") ? <span>{metadataString(chunk.metadata, "chunk_type")}</span> : null}
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

function documentChunkSourceDetails(chunk: RetrievedDocumentChunk["chunk"]) {
  return [
    chunk.section_path?.join(" > ") || metadataString(chunk.metadata, "heading_path") || "document root",
    sourceRangeLabel(chunk.start_offset, chunk.end_offset),
    shortSourceLabel("version", chunk.document_version),
    shortSourceLabel("hash", chunk.content_hash)
  ].filter(Boolean).join(" / ");
}

function sourceRangeLabel(start: number | undefined, end: number | undefined) {
  return typeof start === "number" && typeof end === "number" && end > start ? `bytes ${start}-${end}` : "";
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}
