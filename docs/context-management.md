# Context Assembly and Compaction

Context Assembly owns the input of one logical Model Call. It is a capacity and
explainability mechanism, not a cumulative cost budget. For the distinction,
see [Execution controls](execution-controls.md) and [Run Budget](run-budget.md).

Every model call is assembled against an explicit input budget:

```txt
input budget = model context window - output reserve - safety margin
```

The assembler always keeps required protocol content such as the system prompt, current input, tool calls, tool results, and the current non-empty Structured Task State. Optional conversation history, semantic memory, and RAG knowledge are selected within their source budgets. Each call emits a Context Manifest describing what was selected, excluded, or transformed without persisting raw prompt content in the manifest.

Selected RAG knowledge also carries a per-context citation source ID such as
`S1`. The manifest records that alias on each knowledge entry, allowing final
response citations to be resolved only against knowledge that survived the
assembler's last input-budget check.

Context compaction is non-destructive. It creates a persisted structured summary for older conversation messages; it never deletes or overwrites the original messages. On subsequent model calls, the assembler injects that summary, excludes the covered raw messages, and keeps the recent raw tail.

Source-aware session history retrieval closes the remaining recovery gap. Before
each model call, the Runtime searches the durable conversation Messages and
execution Events using terms from the current input. It can reintroduce exact
older IDs, commands, errors, and nearby sources even when those Messages were
covered by compaction. Retrieved sources are a temporary context projection;
the original Session Log is never changed.

## Selection Contract

| Source | Treatment |
| --- | --- |
| System protocol and current input | required |
| Tool definitions and active tool results | required, with result compaction when oversized |
| Structured Task State | required when a Revision exists; reloaded before every physical Model Call |
| Recent conversation history | selected within the history budget |
| Curated semantic memory | selected within the memory budget |
| RAG knowledge | selected within the knowledge budget and wrapped as untrusted data |
| Persisted conversation summary | required when an active compaction exists |
| Retrieved original session sources | relevant matches within dedicated result, character, token, and input budgets |

Every assembly emits a Context Manifest containing source IDs, selection
reasons, transformations, token estimates, and a stable prefix hash without
copying raw dynamic context into the event.

Structured Task State is injected as bounded JSON and recorded in the Manifest
with a versioned reference such as `conversation_id:v3`. Its raw facts remain in
the immutable Revision snapshot rather than the Manifest. See
[Structured Durable Task State](task-state.md). New Runs enable this protocol
through Runtime Snapshot v8; v1-v7 Runs keep it disabled when resumed.

After assembly and application of effective request limits, the model adapter
persists a Model Request Envelope for each physical attempt. The Manifest
explains source selection; the Envelope hashes the exact final transport
payload; optional Context Capture retains content according to a separate
process policy. The Envelope also freezes selected token totals by source so
the debug API can detect drift from the referenced Manifest. See
[Model request reconstruction](model-request-reconstruction.md).

History entries use stable references such as `message:<message_id>` and
`event:<event_id>`. The Manifest records the reference, source type, selection
reason, transformation, and estimated tokens, but not the raw recovered text.
The injected block labels historical sources as read-only evidence rather than
instructions and gives the current request and system protocol precedence.

## Session History Retrieval

The internal retrieval contract supports keyword, Message ID, Event ID, Event
type, role, and inclusive time-range filters. A direct match may include a
bounded number of adjacent Messages or Events so exact evidence is not returned
without its local context. Runtime auto-retrieval currently derives keywords
from the active Turn and excludes active raw Messages plus all Events from the
current Run. Explicit filters remain available to internal callers.

Only evidence-bearing execution Events are searched automatically, including
tool failures/results, model failures, completed or failed Stages, failed Runs,
verification failures, compaction failures, and budget failures. This prevents
query-bearing retrieval and context trace Events from recursively matching
themselves. Explicit Event ID/type queries can search any persisted Event type.

Retrieval is best effort. Store or search failures emit
`session_history.search_failed` and do not block the primary model call.
Successful calls emit `session_history.search_started` and
`session_history.search_completed` with references and counts, not raw source
content. New Runs freeze the limits in Runtime Snapshot v6; Runs created with
older snapshots keep this behavior disabled when resumed.

## When Compaction Runs

- **Soft compaction** runs asynchronously after a completed Run when context usage reaches 70% of the available model input budget. It prefers the highest real `prompt_tokens` value observed in the Run and falls back to local token estimation.
- **Hard compaction** runs as a best-effort preflight before each model call when estimated conversation context reaches 85% of the available input budget.
- Compaction is skipped when fewer than two new source messages can be summarized, when `CONTEXT_COMPACTION_MODE=off`, or for legacy v1/v2 Runtime Snapshots.
- A failed or timed-out compaction does not block the main model call. AgentFlow records `context.compaction_failed` and falls back to normal recent-history selection.

## Compaction Algorithm

1. Load the original conversation messages and the latest persisted compaction, if one exists.
2. Exclude message IDs already covered by the previous compaction and carry its summary forward as the starting state.
3. Protect the recent raw tail up to `CONTEXT_COMPACTION_RECENT_TOKENS`. At least four messages are retained, and a `user -> assistant` pair is not split at the compaction boundary.
4. Send only the previous summary and newly eligible older messages to the frozen Run model. The model returns a structured summary covering goals, constraints, decisions, facts, completed and pending work, important tool results, errors, and source references.
5. Limit the persisted summary to `CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS` and calculate a source hash for idempotency.
6. Persist the new compaction in File Store or Postgres and emit `context.compaction_started` followed by `context.compaction_completed` or `context.compaction_failed`.

Repeated compactions are incremental: each new summary combines the previous summary with only the newly compactable messages. Original messages remain available for replay and debugging.

## Compression Ratio

AgentFlow does not promise a fixed compression ratio. Each persisted compaction records `before_tokens` and `after_tokens`, where:

```txt
remaining ratio = after_tokens / before_tokens
reduction ratio = 1 - remaining ratio
compression factor = before_tokens / after_tokens
```

`after_tokens` includes both the structured summary and the protected recent raw tail. With the defaults below, the target retained context is approximately `2,000 + 16,000 = 18,000` tokens. For the default 128K model context window, the available input budget is 115,712 tokens:

| Trigger | Approximate context at trigger | Illustrative retained ratio | Illustrative reduction | Compression factor |
| --- | ---: | ---: | ---: | ---: |
| Soft 70% | 80,998 tokens | 22% | 78% | 4.5x |
| Hard 85% | 98,355 tokens | 18% | 82% | 5.5x |

These values are operational examples, not guarantees. The actual ratio varies with message boundaries, the minimum four-message tail, summary size, previous compactions, and whether the trigger came from provider-reported prompt usage that also included tools, memory, or RAG context.

Oversized tool results use a separate deterministic transformation during every context assembly. AgentFlow preserves their head and tail up to `CONTEXT_TOOL_RESULT_MAX_TOKENS`; this is recorded as `tool_result_compacted` in the Context Manifest and is not part of the LLM summary ratio above.

Relevant configuration:

```bash
MODEL_CONTEXT_WINDOW_TOKENS=128000
MODEL_OUTPUT_RESERVE_TOKENS=8192
CONTEXT_SAFETY_MARGIN_TOKENS=4096
CONTEXT_HISTORY_MAX_TOKENS=64000
CONTEXT_MEMORY_MAX_TOKENS=8000
CONTEXT_KNOWLEDGE_MAX_TOKENS=16000
CONTEXT_TOOL_RESULT_MAX_TOKENS=2000
CONTEXT_HISTORY_RETRIEVAL_MAX_RESULTS=8
CONTEXT_HISTORY_RETRIEVAL_MAX_CHARACTERS=12000
CONTEXT_HISTORY_RETRIEVAL_MAX_TOKENS=3000
CONTEXT_HISTORY_RETRIEVAL_WINDOW=1

CONTEXT_COMPACTION_MODE=auto
CONTEXT_COMPACTION_SOFT_THRESHOLD=0.70
CONTEXT_COMPACTION_HARD_THRESHOLD=0.85
CONTEXT_COMPACTION_RECENT_TOKENS=16000
CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS=2000
CONTEXT_COMPACTION_TIMEOUT=45s
```

## Failure Semantics

- Required context exceeding the input budget fails the Model Call rather than
  silently dropping protocol content.
- Optional source overflow records an exclusion reason in the Manifest.
- Hard compaction failure falls back to normal recent-history selection when
  the request can still fit.
- Soft compaction failure is recorded after completion and never changes the
  completed Run outcome.
- Original Messages remain authoritative and are never deleted by compaction.
- Session history retrieval never writes back to Messages or RunEvents. File
  Store performs a bounded in-process scan; Postgres reads the indexed
  conversation event timeline before applying the same retrieval policy.
