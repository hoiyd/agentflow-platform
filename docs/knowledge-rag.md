# Knowledge / RAG

The Knowledge page supports:

- adding a text document from a textarea
- uploading `.txt`, `.md`, and `.markdown` files
- listing documents with title, filename, format, chunk count, and embedding count
- viewing document chunks and Markdown metadata
- deleting documents and their chunks/embeddings
- searching indexed chunks with dense and lexical recall
- seeing embedding provider/model/dimensions used for search

Markdown ingestion is structure-aware:

- headings become `heading_path`
- paragraphs, lists, and fenced code blocks become chunk types
- code block language is preserved when available
- oversized blocks fall back to fixed-size chunk splitting
- heading context is included in chunk content to improve retrieval

## Chunk Source Traceability

Newly ingested documents and chunks carry structured source details:

- `version` is accepted from the ingest request; when omitted it is derived as
  `sha256:<document_content_hash>`.
- Document and chunk `content_hash` values are lowercase SHA-256 hex digests.
- `section_path` is a structured heading path. The existing `heading_path`
  metadata remains available for compatibility and search features.
- `start_offset` and `end_offset` form a half-open UTF-8 byte range into the
  normalized source document. Ingestion normalizes CRLF/CR line endings to LF
  and trims outer whitespace before calculating offsets and hashes.
- Markdown heading context prepended for retrieval is not part of the source
  byte range. Slicing the normalized document by the offsets returns the
  original source block contained in the stored chunk.
- `parent_id` is deterministic for a document version and source section.
  Chunks from the same Markdown section share it; plain-text chunks share the
  document root parent. This prepares grouping for Parent-Child Retrieval.

The document detail API, RAG search results, Agent retrieval events, and model
call traces expose the same source fields. Existing rows are preserved with
empty/default source details; the migration does not invent unverifiable
offsets or hashes for legacy chunks. FileStore persists normalized source
content in its internal data file so offsets remain resolvable after restart;
the full source content is still excluded from HTTP document responses.

## Retrieval Pipeline

HTTP search, Single-Agent, Multi-Agent, and Autonomous runs use the same
`KnowledgeBase` and `RetrievalPipeline` path:

1. Normalize and embed the query with the configured embedding provider/model.
2. Recall independent dense and lexical candidate sets. Both paths apply the
   requested workspace and metadata filters.
3. Merge candidates by chunk ID while preserving `vector_rank`,
   `lexical_rank`, and `lexical_score` recall details.
4. Block candidates that match high-confidence prompt-injection patterns and
   record structured filtering decisions.
5. Fuse the remaining recall rankings with equal-weight Reciprocal Rank Fusion
   (RRF).
6. Apply the existing evidence-aware reranker, metadata and recency signals,
   and document diversity control.
7. Remove low-confidence results with the relevance gate and return the
   requested top K.
8. Use the gated child hits to select model context within a token budget:
   include each matched child, prefer same-parent section chunks, and fall back
   to adjacent chunks when no parent expansion can be selected.

Dense recall uses cosine similarity against document chunk embeddings. Lexical
recall can introduce a chunk that is absent from the dense candidate set, which
improves retrieval of exact error codes, product IDs, API paths, and domain
names. The local file store scores exact phrases, identifier-like terms, and
query-term coverage across content and selected metadata. Postgres uses exact
substring matching plus `simple`-configuration full-text search over generated
`tsvector` columns and GIN indexes.

RRF uses the standard rank constant `k = 60` and equal weights:

```text
rrf_score = sum(1 / (60 + rank_in_recall_path))
```

A missing recall path contributes zero. Candidates are sorted by `rrf_score` to
produce `fusion_rank`; deterministic chunk-ID ordering breaks exact ties. Raw
dense and lexical scores are retained for observability but are not compared
across recall paths. The downstream `rerank_score` starts from a normalized RRF
score and then applies the existing lexical, metadata, evidence, and diversity
signals.

Search and evaluation responses include a top-level `fusion` object containing
the algorithm, version, rank constant, and source weights. Retrieval trace
events record the same object. The Knowledge and Replay interfaces display this
metadata and retain six decimal places for raw RRF scores so adjacent source
ranks remain distinguishable during manual verification.

The UI labels the dense/vector path as **Semantic** and the lexical path as
**Keyword**. These user-facing names map to the existing `vector_rank`,
`lexical_rank`, `dense_weight`, and `lexical_weight` API fields; the wire
contract remains unchanged.

## Parent-Child Context Selection

Retrieval and model context now have separate contracts:

- `items` contains the child chunks that were recalled, fused, reranked, and
  accepted by the relevance gate. RAG evaluation continues to score these
  child hits.
- `context_items` contains the chunks selected for model input. Each item has a
  `context_role` of `matched_child`, `parent`, or `adjacent`, plus the
  `matched_chunk_id` that caused the expansion.
- `context_selection` reports algorithm version `parent-child-v1`, token budget,
  tokens used, role counts, and whether scoped lookup was applied.

`parent_id` identifies a logical source section rather than a separate parent
row. Parent expansion therefore means selecting other chunks from the same
document and section. Candidates nearest to the matched child are considered
first. If no same-parent chunk fits, the selector tries chunks within one
adjacent chunk index. Chunk IDs are deduplicated across all matched children.

HTTP search accepts optional `context_token_budget`; the default is 16,000.
Agent runs use the frozen `ContextAssembly.KnowledgeMaxTokens` value from the
Run snapshot, so retrieval expansion and final context assembly share the same
upper bound. Context Assembly still performs its own final packing check.

Every expansion query requires the matched `document_id`, preserves metadata
filters, and adds `workspace_id` filtering whenever a workspace is available.
The selector validates those fields again before accepting Store results.
This prevents parent/neighbor expansion from widening the original search
scope. It does not replace the planned end-to-end workspace lifecycle and
mandatory production-mode isolation work; until those items are complete, the
feature is supported as single-workspace behavior rather than a multitenant
security guarantee.

Expanded chunks are untrusted just like direct hits. They pass through the
prompt-injection guard before budget selection, and their decisions are merged
into the response and retrieval trace security summary.

## Prompt-Injection Guard

All retrieved knowledge is treated as untrusted external data. The guard uses
policy version `rag-prompt-guard-v1` and blocks high-confidence patterns before
RRF, including:

- requests to ignore, override, or bypass prior/system instructions
- attempts to replace the model role or inject a new system prompt
- requests to reveal system/developer instructions
- explicit requests to invoke tools or execute shell commands
- attempts to spoof AgentFlow's untrusted-knowledge boundary markers

Blocked chunks do not participate in fusion, reranking, context selection, or
model calls. Responses and retrieval traces retain only document/chunk IDs, the
`blocked` action, and reason codes; blocked raw content is not copied into the
security decision.

Context Assembly adds a system-level policy that retrieved knowledge is data,
not instructions. Selected chunks are enclosed in
`<untrusted_knowledge_context>` and `<untrusted_knowledge_document>` markers,
and the current user request is placed after the retrieved context. This trust
boundary remains active even when no injection pattern is detected.

The deterministic detector intentionally favors precision over broad semantic
classification. It is one defense layer, not a claim that every possible
natural-language attack can be recognized. The system-role trust policy,
content boundary, filtering, and audit trail provide defense in depth.

## Search Semantics

- Omitting `min_similarity`, or setting it to `0`, allows lexical-only chunks
  into the merged candidate set.
- Setting a positive `min_similarity` preserves the original vector-threshold
  contract: lexical data can enrich a dense hit, but cannot introduce a chunk
  that did not pass dense recall.
- `lexical_score` is the lexical recall score. `lexical_boost` is a separate
  reranker feature calculated after candidate merging.
- `rrf_score` is the raw reciprocal-rank sum and `fusion_rank` is its ordering
  before the heuristic reranker assigns `rerank_rank`.
- A lexical-only result has `similarity: 0` and no `vector_rank`; a dense-only
  result has no `lexical_rank` or `lexical_score`.
- `no_match` remains true when all recalled candidates fail the relevance gate.
