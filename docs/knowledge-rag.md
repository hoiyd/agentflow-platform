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
4. Apply the existing evidence-aware reranker, metadata and recency signals,
   and document diversity control.
5. Remove low-confidence results with the relevance gate and return the
   requested top K.

Dense recall uses cosine similarity against document chunk embeddings. Lexical
recall can introduce a chunk that is absent from the dense candidate set, which
improves retrieval of exact error codes, product IDs, API paths, and domain
names. The local file store scores exact phrases, identifier-like terms, and
query-term coverage across content and selected metadata. Postgres uses exact
substring matching plus `simple`-configuration full-text search over generated
`tsvector` columns and GIN indexes.

This stage does not implement Reciprocal Rank Fusion (RRF). Dense and lexical
candidates are deduplicated, then ordered by the shared heuristic reranker. RRF
is a separate planned change.

## Search Semantics

- Omitting `min_similarity`, or setting it to `0`, allows lexical-only chunks
  into the merged candidate set.
- Setting a positive `min_similarity` preserves the original vector-threshold
  contract: lexical data can enrich a dense hit, but cannot introduce a chunk
  that did not pass dense recall.
- `lexical_score` is the lexical recall score. `lexical_boost` is a separate
  reranker feature calculated after candidate merging.
- A lexical-only result has `similarity: 0` and no `vector_rank`; a dense-only
  result has no `lexical_rank` or `lexical_score`.
- `no_match` remains true when all recalled candidates fail the relevance gate.
