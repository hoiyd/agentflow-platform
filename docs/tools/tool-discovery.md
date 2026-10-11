# Toolsets and Lazy Schema Discovery

Tool discovery changes what the model sees, not what it can execute. Single,
Multi Worker and Autonomous Act share the existing Tool loop and Executor.

## Configuration

```dotenv
TOOL_SCHEMA_MODE=eager
TOOL_SCHEMA_TOKEN_THRESHOLD=2048
```

| Mode | Model-visible definitions |
| --- | --- |
| `eager` (default) | All ready, authorized definitions, as before |
| `lazy` | Core definitions plus `tool_search`; matched full schemas enter the next request |
| `auto` | Lazy only when full schemas reach the estimated-token threshold and the initial lazy request is cheaper; otherwise eager |

The threshold is a tuning parameter, not a Context limit or a provider token
count. Auto chooses once per execution scope, not on every round. An empty
Catalog stays eager. Existing snapshots without `tool_schema` remain eager;
new Runs freeze the normalized configuration. Changing environment variables
does not reinterpret an old Run. No SQL migration or Snapshot version bump is
needed: the optional field and typed events use existing JSON persistence.

## Protocol and Bounds

```text
authorized frozen Catalog -> bounded naming-group index + allowed core tools
  -> tool_search(query) -> matched names/revisions/evidence
  -> persist activation -> next request includes full matched schemas
  -> ordinary Tool call -> Executor -> paired observation -> answer
```

Core names are `get_current_time`, `skill_load`, `skill_read`, and
`update_task_state`, only when already authorized and ready. Other definitions
stay in the frozen Catalog until loaded. Toolsets are derived from the naming
prefix before `_`; they are display/search groups, never group-wide grants.

Search is deterministic lexical matching over names, descriptions and parameter
schemas. Names receive higher weight; ties use name order. Results include
identity, revision, source, toolset and matched terms, not full schemas.
No semantic/regex execution, network request, global-Catalog fallback or
automatic authority expansion occurs. An empty match returns `[]`.

| Bound | Value / scope |
| --- | --- |
| Query | 1-256 Unicode characters, validated by the normal argument contract |
| Results | At most 3 per search |
| Searches | At most 8 per Run/Stage/Agent, including misses; restored on Resume |
| Active target schemas | At most 32 per Run/Stage/Agent, plus `tool_search` |
| Index summary | At most 4096 bytes; search still covers unlisted authorized names |

Core definitions precede matched definitions in activation order. Repeated
matches are idempotent. Calling a deferred Tool before activation, even beside
a search in the same batch, yields `tool_not_loaded` and executes nothing.
Activation happens after the whole batch. Search consumes normal Tool/Run
budgets and Progress Guard allowances; query errors can be corrected in the
next round, but budget, cancellation and persistence failures stop execution.

## Authorization, Recovery and Evidence

Current availability, Workspace/Agent grants, definition revision and policy
are checked on discovery, activation, before subsequent model requests and on
execution. The runtime-only index read has its own exact local-read rule; that
rule cannot authorize a matched target. Existing scope, credential, Journal,
artifact and private-data boundaries remain authoritative.

`tool.discovery.updated` persists the candidate identity/revision digest,
ordered active identities, search count, reason and estimated full/visible
schema costs. State is isolated by Run/Stage/Agent. Resume restores the latest
matching state and rejects drift, malformed bounds or unavailable definitions.
A failed state write prevents further requests. This is visibility recovery,
not permission restoration or replay of a partially executed Tool batch.

Lazy calls use per-Tool occurrence slots, so skipping an already-completed
search on same-Stage retry does not change a target's journal identity.
Committed effects replay; changed arguments remain rejected. New-Stage Resume
continues to follow the existing checkpoint/reconciliation boundary.

Replay exposes discovery events alongside ordinary Tool outcomes. Each request's
Manifest and optional Request Capture describe its actual visible schemas;
previous requests are not rewritten. Run comparison treats discovery settings
as part of the Tool-set experimental dimension.

## Validation and Limitations

Backend integration tests cover wire activation, rejection/correction, revoked
access, state corruption, bounded searches and Postgres close/reopen recovery.
`TestDiscoveryControlledSchemaCostEvidence` compares the same task and synthetic
Catalog under eager/lazy, recording physical request hashes, counts, estimated
schema costs and fixture latency. Retain its verbose/JSON test output as evidence.
The [browser gate](../operations/functional-regression-testing.md#tool-discovery)
covers all three modes through production Go composition and disposable Postgres.

These are deterministic fixture results, not calibrated live-model selection
quality, tokenization, pricing or throughput. Eager remains the default until
representative tasks justify the extra search round trips. Native provider Tool
Search is not implemented: this uses ordinary Chat Completions `tools` arrays.
Changing that array may invalidate prompt caches; cached usage and benefits
are **unknown**, not promised. Skill package discovery/limits are unchanged.
