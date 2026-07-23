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

## Retrieval Pipeline

HTTP search, Single-Agent, Multi-Agent, and Autonomous runs use the same
`KnowledgeBase` and `RetrievalPipeline` path:

1. Normalize and embed the query with the configured embedding provider/model.
2. Recall independent dense and lexical candidate sets. Both paths apply the
   requested workspace and metadata filters.
3. Merge candidates by chunk ID while preserving `vector_rank`,
   `lexical_rank`, and `lexical_score` provenance.
4. Block candidates that match high-confidence prompt-injection patterns and
   record structured filtering decisions.
5. Fuse the remaining recall rankings with equal-weight Reciprocal Rank Fusion
   (RRF).
6. Apply the existing evidence-aware reranker, metadata and recency signals,
   and document diversity control.
7. Remove low-confidence results with the relevance gate and return the
   requested top K.

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
