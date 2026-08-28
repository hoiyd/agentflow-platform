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
- **Provider-overflow compaction** is forced after a text-only Model Call returns `context_length_exceeded`. AgentFlow retries that logical input once, and only when the persisted compaction generation advanced. Agent streams that may have executed Tools are not replayed automatically.
- Compaction is skipped when fewer than two new source messages can be summarized, when `CONTEXT_COMPACTION_MODE=off`, or for legacy v1/v2 Runtime Snapshots.
- A failed or timed-out compaction does not block the main model call. AgentFlow records `context.compaction_failed` and falls back to normal recent-history selection.
- Temporary summarizer failures use a one-minute cooldown; authentication, quota, validation, and missing-model configuration failures use a 15-minute cooldown. Two consecutive compactions below 10% reduction suspend automatic compaction for 30 minutes.

## Compaction Algorithm

1. Load the original conversation messages and the latest persisted compaction, if one exists.
2. Exclude message IDs already covered by the previous compaction and carry its summary forward as the starting state.
3. Protect the recent raw tail up to `CONTEXT_COMPACTION_RECENT_TOKENS`. At least four messages are retained. A user exchange, including assistant Tool Calls and their Tool Results, is an indivisible protocol group and is never split at the compaction boundary.
4. Send only the previous summary and newly eligible older messages to the frozen Run model. The schema explicitly records superseded instructions, uncertainties, conflicts, exact references, and missing evidence. Newer corrections override older summary statements, while the current user request remains outside the historical summary and always has priority when assembled.
5. Calculate a dynamic target summary budget as approximately 20% of eligible source tokens, with a 256-token useful floor when the configured cap permits it and `CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS` as the hard ceiling.
6. Assign an immutable generation and persist exact source message/event IDs, the replacement summary ID, previous generation link, source hash, and shadowed message range. Original Messages and Events are retained.
7. Emit `context.compaction_started`, then atomically persist and activate the summary surface together with `context.compaction_completed`. A stale unmatched start is repaired as `context.compaction_failed`; it never makes a partial summary visible or produces a false completion.

Repeated compactions are incremental: each new summary combines the previous summary with only the newly compactable messages. The Context Manifest records the active compaction ID and generation. Summaries are injected as historical references; Structured Task State and the current user request win on conflict. Original messages remain available for replay and debugging.

### Exact v2 Selection Algorithm

The current implementation is `context-compaction-v2`. It decides whether to
compact using the following values:

```txt
input_budget = context_window - output_reserve - safety_margin
estimated_surface = tokens(previous_summary) + tokens(uncovered_messages)
trigger_tokens = max(estimated_surface, observed_provider_prompt_tokens)

soft_threshold = input_budget * CONTEXT_COMPACTION_SOFT_THRESHOLD
hard_threshold = input_budget * CONTEXT_COMPACTION_HARD_THRESHOLD
```

`observed_provider_prompt_tokens` is available to the asynchronous soft trigger
and can include system prompts, Tools, Memory, and RAG sources. The local
`estimated_surface` only measures the rolling conversation summary and original
Conversation Messages. A provider-overflow trigger is forced and does not use a
percentage threshold.

After loading the latest completed generation, the planner performs these steps:

1. Build `covered_ids` from the previous generation's cumulative
   `source_message_ids`.
2. Build `active_messages` from original Messages whose IDs are not covered.
   Covered Messages remain in storage; they are excluded only from the visible
   model-input surface.
3. Form protocol groups. A `user` Message starts a group, and every following
   assistant or Tool protocol Message remains in that group until the next
   `user` Message. The compaction boundary can only fall between groups.
4. Walk groups from newest to oldest to construct the protected raw tail. Keep
   at least four Messages. After that minimum is satisfied, stop before adding
   the next group when it would exceed `CONTEXT_COMPACTION_RECENT_TOKENS`.
   Preserving a complete group may therefore exceed the configured target.
5. Select everything before the protected tail as `new_source_messages`.
   Compaction is skipped when fewer than two new Messages are eligible.
6. Build cumulative source lineage as `previous source IDs + new source IDs`,
   remove duplicate IDs while preserving order, and hash each selected
   `(message_id, role, content)` tuple for idempotency.

In simplified pseudocode:

```txt
active = original_messages - previous.source_message_ids
groups = group_from_each_user_until_next_user(active)
protected_tail = take_complete_groups_from_newest(
  minimum_messages = 4,
  target_tokens = CONTEXT_COMPACTION_RECENT_TOKENS,
)
new_sources = active before protected_tail

if len(new_sources) < 2:
  skip
```

### Dynamic Summary Budget

The summary target scales with the newly eligible source surface instead of
always requesting the configured maximum:

```txt
eligible_source_tokens = before_tokens - protected_tail_tokens

when summary_max_tokens >= 256:
  target_summary_tokens = clamp(
    eligible_source_tokens / 5,
    256,
    summary_max_tokens,
  )

when summary_max_tokens < 256:
  target_summary_tokens = summary_max_tokens
```

This targets roughly 20% of eligible source tokens, uses 256 tokens as a useful
floor when the hard cap permits it, and always treats
`CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS` as the ceiling. A response above the
target is deterministically truncated before persistence.

The summarizer receives only the previous summary and newly eligible Messages.
It must return the standard operational sections plus:

- `Superseded Instructions`: corrections, canceled tasks, and instructions that
  must not be revived;
- `Uncertainties`: facts that remain uncertain;
- `Conflicts`: incompatible claims that cannot be resolved from the sources;
- `Exact References`: identifiers, commands, paths, and values that must remain
  verbatim;
- `Evidence Needed`: missing evidence required to resolve open work.

Newer source Messages override conflicting statements in the previous summary.
The resulting summary is still historical evidence: Structured Task State and
the current user request take priority when the assembler injects it.

### Generation And Commit Protocol

Every successful compaction creates one immutable generation:

```txt
generation = previous_generation + 1
replacement_summary_id = "summary:" + compaction_id
```

The durable record includes the previous compaction ID, cumulative source
Message IDs, exact shadowed first/last Message IDs, source count, source hash,
target budget, token estimates, reduction ratio, summary model, and algorithm
version. Postgres also enforces one generation number per Conversation.

The lifecycle is deliberately asymmetric around the model request:

```txt
repair stale unmatched starts
  -> persist context.compaction_started
  -> call summarizer
  -> atomically commit completed summary surface + context.compaction_completed
```

If summarization fails, AgentFlow writes `context.compaction_failed` and does not
create a visible summary. If the process crashes after `started` but before the
atomic commit, the attempt is an orphan. A later preflight closes it as failed
after `max(1 minute, 2 * CONTEXT_COMPACTION_TIMEOUT)`. The assembler reads only
completed generations, so an orphan cannot become the active surface.

### Effectiveness And Retry Guards

After a successful commit:

```txt
after_tokens = tokens(summary) + protected_tail_tokens
reduction_ratio = (before_tokens - after_tokens) / before_tokens
```

A reduction below 10% is low yield. Two consecutive low-yield generations pause
automatic compaction for 30 minutes. The counter resets when that cooldown
expires or a useful compaction succeeds.

Summarizer timeouts and temporary availability failures use a one-minute
cooldown. Authentication, quota, validation, missing-model, and missing
summarizer configuration failures use a 15-minute cooldown. Cooldown metadata is
recorded in lifecycle events and restored after process restart.

For a text-only Model Call rejected with `context_length_exceeded`, AgentFlow:

1. forces one compaction attempt for the current Turn ID;
2. reloads the persisted generation;
3. retries the Model Call only when the generation increased;
4. refuses another automatic recovery for the same Turn ID.

Agent streams are not automatically replayed because Tool side effects may have
already occurred. A new logical Turn supplies a new overflow guard key.

### Current Scope

The v2 rolling summarizer currently selects persisted Conversation Messages as
its semantic sources, so `source_event_ids` is an exact empty set today. Runtime
Events remain available for replay and tracing but are not summarized into the
conversation surface. Memory, RAG context, Tool definitions, and oversized Tool
Results use their own assembler budgets and transformations; they are not folded
into the rolling summary.

Compaction is intentionally lossy. Generation lineage and retained originals
make the transformation explainable and reversible for debugging, but they do
not make a generated summary semantically lossless.

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
- A provider-overflow retry occurs at most once for the same Turn ID. A new Turn,
  produced by new user input or a Tool Result, supplies a new guard key.
- Compaction completion is an atomic Store operation across the immutable
  summary surface and terminal event. Orphaned starts are safely closed as
  failures after twice the configured timeout, with a one-minute minimum grace.
- Original Messages remain authoritative and are never deleted by compaction.
- Session history retrieval never writes back to Messages or RunEvents. File
  Store performs a bounded in-process scan; Postgres reads the indexed
  conversation event timeline before applying the same retrieval policy.
