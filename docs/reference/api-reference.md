# HTTP API Reference

This page maps endpoints and operational semantics. Exact request/response DTOs
are owned by [OpenAPI](../../api/openapi.yaml), not repeated JSON examples here.
See [contract generation](../architecture/api-contract.md) before changing them.
Chat/Continue/Resume use SSE; inspection and resource endpoints use JSON.

## Access and Workspace Scope

Default `AUTH_MODE=local` is unauthenticated trusted development. Enabled
`AUTH_MODE=oidc` verifies revocable sessions and Workspace membership before
business requests (401/403), with Origin checks for cookie-authenticated mutations.
See [identity setup and limitations](../operations/identity-membership.md).
Object ACLs remain a separate boundary; `ALLOWED_ORIGINS` alone is not access control.

Business `/api/*` operations resolve `X-Workspace-ID`, `workspace_id` query, or
supported payload scope. Multiple explicit selectors must agree or return 400.
Omission and legacy `default` select `default_workspace`, never a global search.
Conversation, Run, Messages, Memory, Knowledge, Replay, Usage, and Verification
remain scoped; another namespace's resource ID is treated as not found.
Workspace selection is not proof of identity or Membership. In OIDC mode the
server checks the selected namespace against persisted memberships and forbids
payload-only override. Authentication endpoints are not Workspace-scoped.

## Endpoint Map

```txt
GET    /health

GET    /api/auth/session
GET    /api/auth/login
GET    /api/auth/register
GET    /api/auth/callback
POST   /api/auth/logout

GET    /api/conversations
POST   /api/conversations
PATCH  /api/conversations/{id}
DELETE /api/conversations/{id}
GET    /api/conversations/{id}/messages
GET    /api/conversations/{id}/task-state
PATCH  /api/conversations/{id}/task-state
GET    /api/conversations/{id}/task-state/revisions
GET    /api/conversations/{id}/task-state/revisions/{version}

POST   /api/chat

GET    /api/skills
GET    /api/agents
POST   /api/agents
GET    /api/agents/{id}
PATCH  /api/agents/{id}
DELETE /api/agents/{id}

GET    /api/runs
GET    /api/runs/attention
GET    /api/runs/{id}
POST   /api/runs/{id}/continue
POST   /api/runs/{id}/resume
POST   /api/runs/{id}/cancel
POST   /api/runs/{id}/verify
GET    /api/runs/{id}/collaboration_steps
GET    /api/runs/{id}/replay
GET    /api/runs/{id}/projection
GET    /api/runs/{id}/events
GET    /api/runs/{id}/usage
GET    /api/runs/{id}/model_requests
GET    /api/runs/{id}/artifacts
GET    /api/runs/{id}/artifacts/{artifact_id}
GET    /api/runs/{id}/artifacts/{artifact_id}/search
GET    /api/runs/{id}/tool-effects
POST   /api/runs/{id}/tool-effects/{idempotency_key}/reconcile
GET    /api/runs/{id}/episode

GET    /api/tools
POST   /api/tools/{name}/enable
POST   /api/tools/{name}/disable

POST   /api/memories
POST   /api/memories/search
GET    /api/memories/{id}
POST   /api/memories/{id}/mutations

GET    /api/documents
POST   /api/documents
POST   /api/documents/upload
GET    /api/documents/{id}
DELETE /api/documents/{id}
POST   /api/rag/search
POST   /api/rag/evaluations/run
```

## Conversations and Agent Profiles

- Conversation PATCH changes `title`; deletion removes Messages and associated
  execution records from PostgreSQL.
- Task State GET returns the latest state, including empty version 0 before an
  update. PATCH accepts ordered operations and `expected_version`; a stale
  version returns 409/`task_state_version_conflict` without mutation. Revisions
  are immutable, ordered by version, and include patch, resulting snapshot,
  actor/Run metadata, and commit time. See [Task State](../runtime/task-state.md).
- Agent create/update supports prompt, routing hints, Tool allowlist,
  Memory/RAG switches, and Skill names. PATCH changes only supplied fields.
  Installed-but-disabled Tool names are valid allowlist entries; they do not
  become executable automatically. DELETE archives custom Agents, not built-ins.
  See [profiles](../runtime/agent-profiles.md) for the contract and mode behavior.
- `GET /api/skills` returns trusted metadata (name, description, hash, optional
  required Tools), not paths or content. Omitting `skills` in Agent PATCH retains
  bindings; `[]` clears them. Unknown Skills or missing required Agent Tools
  return typed 400 errors. Invocation uses a bound `/skill:name` prefix or native
  `skill_load`/`skill_read`. See [Trusted Skills](../tools/trusted-skills.md).

## Chat, Runs, and Verification

| Operation | Semantics |
| --- | --- |
| Chat | Creates a Run with frozen effective Agent(s), routes, Tools, context, and budget. Mode is `single`, `multi_agent`, or `autonomous`. |
| Continue | Approves/edits a Multi plan; optional routing requirements restrict frozen candidates without granting authority. |
| Resume | Continues the same Run from a human/recovery checkpoint using its Snapshot. Stale/non-recoverable requests or unresolved Tool Effects return 409. |
| Cancel | Explicit backend cancellation; browser disconnect alone does not cancel an admitted Run. |
| Verify | Retries the frozen contract against the latest persisted candidate within its attempt cap; ordinary uncontracted Runs return 409. |
| Collaboration steps | Persisted Stage records; each `CollaborationStep.id` is its event `stage_id`. Workers have no separate Child Run API. |
| Attention | Derived highest-priority operator item per actionable Run, with Evidence, existing Recovery Action, and `observation_sequence`; no second persisted status. |

The initial non-null `completion_contract` is the only Verification opt-in.
Mode and `VERIFICATION_*` settings do not enable it. Ordinary Runs remain
`not_required`; contracted Runs inherit the frozen policy on Continue/Resume.
Terminal `done` reports Run `status` and `verification_status` separately.
The seven built-ins and normalized `verifiers[].config` shapes are documented in
[Verification](../runtime/verification.md), not development test results.

Router errors distinguish conflicting requirements
(`agent_route_requirements_invalid`), no eligible frozen candidate
(`agent_route_no_eligible_candidate`), and candidates below the suitability
gate (`agent_route_no_suitable_candidate`). They do not start a Worker;
`agent.selection.decided` preserves the decision. See
[Agent selection](../runtime/agent-selection.md).

## Usage and Replay

| Read surface | Contract / owner |
| --- | --- |
| Replay | Detailed aggregate: Snapshot, Messages, Stages, durable Events, Trace Summary, Usage, ordered Task State revisions, Verification, and Tool Artifact metadata. |
| Projection | Deterministic Run/Usage/Verification read models at `as_of_sequence`, with report-only `invariant_failures`. Not a second truth store or checkpoint. Replay includes it and retains top-level `summary`/`usage_ledger`. |
| Recovery summary | Present for stopped/actionable Runs; contains reason, Evidence, partial-output references, and actions with availability reasons. See [recovery](../runtime/durable-recovery.md). |
| Usage | Immutable budget, effective totals, open reservations, and append-only entries. Settlement replaces the estimate for the same `operation_id`. See [Run Budget](../runtime/run-budget.md). |
| Model requests | Physical-attempt Envelopes, Capture metadata, Manifest references, token-source diffs, and reconstruction invariants. Content requires `include_content=true` and valid retention. See [capture](../context/model-request-reconstruction.md). |
| Artifacts | Lists/Replay expose metadata only. Bounded reads use `offset`/`limit`; search uses `q`/`max_matches`. Expired content returns 410. See [Tool Artifacts](../tools/tool-result-artifacts.md). |
| Episode | Compact export derived from Replay, not another execution history or accounting source. |

`GET /api/runs/{id}/events` observes an admitted Run independently of its
starting request. `Last-Event-ID` takes precedence over `after` as the durable
sequence cursor. The stream hands off replayed Run Events, a `run.snapshot`
at its projection watermark, and subsequent live events without a read/subscribe
gap. It closes when execution completes, fails, cancels, or waits for a human.
Reconnect with the latest SSE ID and reload persisted Messages when stopped.
`model.delta` is live-only; partial tokens are not durable replay history.
See [event projections](../runtime/event-projections-runtime-invariants.md).

HTTP/SSE errors retain `error` and add stable `code`, `source`, `category`,
`retryable`, optional `operation`, and `request_id`. Dynamic 5xx responses
contain standard HTTP text, not internal exception details. Episode trace errors
may include `kind`/`category`/`retryable`; historical events without those
fields remain readable. See [failure handling](../runtime/failure-handling.md).

## Tool Governance

Platform enablement persists to `TOOL_CONFIG_PATH` and is separate from Agent
allowlists, runtime readiness, and operator scope. Authorization precedes Budget
and Handler execution; allowlists cannot widen Resource, Network, or Credential
scope. See [Tool security](../tools/tool-security-policy.md).

Tool-effect inspection supports exact `tool`/`status` filters, hides result
bodies, and returns version plus supported actions. Reconciliation accepts
`command_id`, `action`, `expected_version`, `actor`, `reason`, and a bounded
JSON `result` for `confirm_committed`. Stale/unavailable actions return 409;
repeating an applied command is idempotent with `applied=false`. Typed audit
records expose success/failure. The state machine and operator trust boundary
are in [reconciliation](../tools/tool-side-effect-reconciliation.md).

## Documents and RAG Search Response

Document ingestion accepts a Workspace-local `source_key` (512-byte maximum;
defaults to `source_uri`). Exact retries are idempotent. Reusing a key/version
with changed content returns 409/`document_version_conflict`; a new version
atomically replaces the index and preserves the document ID. Failed replacement
keeps the old index. Deletion removes chunks and embeddings too.

Search fails with 409/`knowledge_index_incompatible` for unknown/mixed active
chunker or embedding identities. Reindex or delete incompatible sources;
the server does not search partial or mixed vector spaces.

| Response field | Meaning |
| --- | --- |
| `items` | Ranked, gated child hits; includes document/chunk identity, version/hash, section path, parent, and half-open UTF-8 source offsets. |
| `vector_rank` / `lexical_rank` | Independent recall positions; absent paths omit their rank. Keyword-only hits have `similarity: 0`. |
| `rrf_score` / `fusion_rank` | Equal-weight reciprocal ranks with k=60; absent paths contribute zero. Raw recall scores are not mixed. |
| `rerank_rank` / `rerank_score` | Final ordering after lexical, metadata, evidence, recency, and diversity signals. `lexical_score` differs from `lexical_boost`. |
| `embedding` / `fusion` / `reranker` / `relevance_gate` | Actual versioned pipeline identities and configuration. |
| `relevance_decisions` | Content-free candidate scores, evidence coverage, matched terms/identifier, confidence, acceptance, and reason. |
| `context_items` | Model-selected chunks, not evaluation hits; matched-child/parent/adjacent roles, source IDs, and merged provenance. |
| `context_selection` | Algorithm, cap/used tokens, role counts, scope check, and deduplication/merge transformation counts. |
| `citation_sources` | Trusted selected-source aliases; standalone search aliases are response-local. |
| `security` | Version, candidate counts, and content-free blocked decisions for recall and expansion. |
| `no_match` | Successful decision that no candidate passed policy, not proof that the corpus has no answer. |

Zero/omitted `min_similarity` permits Keyword-only recall; a positive value
retains vector-threshold behavior. Embedding/Store errors are execution failures,
not `no_match`. Algorithms, source normalization, expansion, deduplication,
injection filtering, and citation semantics are owned by
[Knowledge / RAG](../knowledge/knowledge-rag.md).

Knowledge `[S#]` and Web `[W#]` citations use separate selected-evidence
protocols. Completion returns only resolved sources; unknown/excluded markers
remain diagnostic invalid IDs. The optional `citation` verifier checks external
Markdown links, while `grounded_answer` checks selected Knowledge claim support.
Neither ordinary URLs nor a search locator alone become trusted citation evidence.

## RAG Evaluation

`POST /api/rag/evaluations/run` executes the configured retrieval pipeline
against the current indexed corpus. The dedicated `/evaluations/retrieval`
page is linked from Knowledge; this is not the isolated offline fixture.

The request accepts exactly one of a versioned `dataset` or legacy top-level
`cases` (document IDs, chunk IDs, and content-match compatibility). Unknown
fields are rejected. [Dataset v1](../schemas/rag-golden-dataset-v1.schema.json)
requires stable ID/version, unique Case IDs, query/answerability, and expected
sources for answerable cases; no-answer cases cannot declare expected sources.

Source selectors AND their document/chunk/URI/content fields; alternative source
definitions match independently. `required_source_count` defaults to one and
cannot exceed expected sources; best rank is where the required distinct
sources have accumulated. Any forbidden Top-K source fails a case; no-answer
passes only with no result. Scope, Top-K, similarity, and acceptable rank keep
cases repeatable.

The response records dataset and pipeline identity, ranked items, per-case
rank/failure/security decisions, Hit@1/3/5, misses, and answerable/unanswerable
counts. Only answerable cases enter Hit@K. MRR, NDCG, no-answer Precision/Recall,
latency, and release gates belong to the offline report, not this API contract.
See [RAG dataset](../evaluation/rag-golden-dataset.md) and
[offline evaluation](../evaluation/offline-evaluation.md) for corpus, version
rules, and opt-in semantic evaluation; no online Evaluation Registry is added.
