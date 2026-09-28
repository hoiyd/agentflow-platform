# Web Source Citation Protocol

`[W#]` identifies a `web_search` result within one Run. It is independent of Knowledge `[S#]` citations and does not change the RAG `citations` API.

Successful, complete `web_search` Tool events supply the catalog. Results receive deterministic Run-scoped IDs in Tool event order; normalized duplicate HTTPS URLs share an ID. Before the follow-up model call, its Tool results are relabeled with those IDs. The final answer resolves a marker only when its corresponding Tool result was fully selected in the final model call's Context Manifest. Failed or truncated Tool results, compacted or excluded context, invented IDs, and URLs copied directly into text cannot become structured citations.

An assistant Message and terminal chat event expose `web_citations` (`source_id`, `title`, `url`, `run_id`, `tool_call_id`, `tool_event_id`). Invalid markers are reported in `invalid_web_citation_ids` and a `citation.resolved` Run event with `protocol_version=web-citation-v1`; the event also records available/cited IDs and the Message ID. Postgres persists Web citations separately from RAG citations. Run Replay reads the persisted Message and links each Web source to its original Tool event.

URL validation checks syntax, HTTPS scheme, host, userinfo and port. Normalization lowercases the host, removes the default HTTPS port and fragment, and preserves path and query. It does **not** fetch the page or assert that its content is true.
