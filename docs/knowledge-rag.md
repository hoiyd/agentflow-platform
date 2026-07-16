# Knowledge / RAG

The Knowledge page supports:

- adding a text document from a textarea
- uploading `.txt`, `.md`, and `.markdown` files
- listing documents with title, filename, format, chunk count, and embedding count
- viewing document chunks and Markdown metadata
- deleting documents and their chunks/embeddings
- searching indexed chunks with a minimum similarity threshold
- seeing embedding provider/model/dimensions used for search

Markdown ingestion is structure-aware:

- headings become `heading_path`
- paragraphs, lists, and fenced code blocks become chunk types
- code block language is preserved when available
- oversized blocks fall back to fixed-size chunk splitting
- heading context is included in chunk content to improve retrieval

Search flow:

1. Query is embedded with the configured embedding provider/model.
2. Vector search retrieves a larger candidate set.
3. Backend rerank applies lexical boost, metadata boost, recency, and diversity control.
4. Results return similarity, score, rerank score, and boost components.
