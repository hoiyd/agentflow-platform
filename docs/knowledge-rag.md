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
4. Fuse the two recall rankings with equal-weight Reciprocal Rank Fusion (RRF).
5. Apply the existing evidence-aware reranker, metadata and recency signals,
   and document diversity control.
6. Remove low-confidence results with the relevance gate and return the
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
