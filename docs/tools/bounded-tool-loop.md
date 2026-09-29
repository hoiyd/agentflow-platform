# Bounded Multi-Round Tool Loop

## Contract

One Turn may contain multiple model requests and guarded Tool batches:

```text
assemble context -> model response
  -> Tool calls -> execute -> paired observations -> assemble again
  -> no Tool calls -> final answer
```

Single, the isolated Multi Worker Stage, and Autonomous Act use the same loop.
The Autonomous iteration count is not a Tool-loop limit. Each request uses the
frozen Tool definitions and existing Context Assembler, Manifest, Request
Capture, Run Budget, Usage Ledger, and Progress Guard.

Tool-enabled rounds use native streaming completion (`stream: true`, frozen
`tools`, `tool_choice: auto`). Answer chunks are forwarded immediately, even
when enabled Tools are not used. No extra answer-generation request or typing
animation is added. Tool-free and explicitly simulated Chat keep their paths.

Until a round finishes, streamed answer text is provisional: a provider may
emit commentary before choosing a Tool. On the first Tool-call fragment, any
already-visible text from that round is retracted with `model.delta` payload
`{ "delta": "", "reset": true }`. Later commentary in that Tool round is
kept for provider continuation, not appended to the answer. Runtime output,
the HTTP response accumulator and the Chat draft all apply the reset; only the
final answer is persisted as the assistant Message. Consumers must clear the
current draft before appending `delta` when `reset` is true; the absent flag
retains the existing append behavior. It is a stream-only draft edit, not a
deletion of historical Messages or durable evidence.

While the active assistant draft is empty, Chat shows `Working...` with a small
loading indicator. It disappears when answer text arrives or execution stops;
it is a generic waiting state, not a claim that the model is currently thinking.

Tool calls are assembled by `delta.tool_calls[].index`, including name and JSON
argument fragments. Execution waits for `[DONE]`, a consistent `tool_calls`
finish reason and validation of the entire batch. No partial arguments execute,
and missing identity is rejected rather than synthesized. Individual SSE frames
and aggregate Tool-round content/continuation state are capped at 1 MiB; Tool
indices must be contiguous from zero and below 1024. The shared protocol follows
the [OpenAI streaming Tool-call contract](https://developers.openai.com/api/docs/guides/function-calling#streaming).

Each round retains the existing frozen **Completion** sampling profile (not
AnswerStream merely because transport is streaming), Manifest, logical call
identity, Run Budget and Usage Ledger. Provider usage is collected through
`stream_options.include_usage`; an unsupported-option response can retry without
that option under the same logical reservation. Missing usage stays explicitly
estimated. Stream attempts can measure first content/Tool-fragment arrival.
Once a visible delta has been sent, the adapter does not automatically retry a
failed stream: partial output remains a failed candidate, never duplicated or
reported as completed. Providers must support streaming native Tool calling;
there is no silent non-streaming or Tool-disabling fallback.

Scope: Single Chat delivers these chunks through the existing Chat/Run SSE and
frontend draft. Multi Worker and Autonomous Act use the same streaming Tool
protocol internally; their orchestration still chooses when to publish the
final reviewed/Decide output. This change does not make every planning,
reviewing or finalizing Stage token-streamed, nor add thinking-text UI.

### Thinking-model continuation

OpenAI-compatible providers may return `message.reasoning_content` alongside
`content` and `tool_calls`. The shared loop retains this optional field verbatim
on each assistant Tool-call message for subsequent requests in the same Turn,
including an explicitly returned empty string. Providers that omit it receive
no additional field. Context Assembly counts its bytes and estimated tokens;
required Tool-call messages are not independently truncated to remove reasoning.
If they exceed the input budget, assembly fails before another model request.

Reasoning is provider continuation state, not answer text. It is neither emitted
to Chat nor copied into ordinary model events. Request Capture retains the
outgoing field only under the existing opt-in `full`/`redacted` body policy;
`metadata_only` does not store it. Provider-reported usage stays authoritative,
and estimated Tool-stream usage includes the serialized request (including Tool
definitions), returned reasoning, and generated Tool names/arguments when usage
is absent. This remains a heuristic, not provider-reported tokenization.

Boundary: this is same-Turn continuation, not durable reasoning history. Chat
history stores final answers, not complete provider response messages, and Stage
retry still restarts the Turn. It does not promise providers' cross-Turn reasoning
requirements, preservation of other opaque fields such as `reasoning_details`,
or thinking-token streaming. See the provider's
[thinking-mode contract](https://api-docs.deepseek.com/guides/thinking_mode/).

## Failure Inventory and Test Plan

Streaming acceptance: a flushed answer delta must reach the consumer while the
provider is still waiting to send later chunks and `[DONE]`. Test this with a
channel handshake, not a sleep or a typing animation. Cover enabled-but-unused
Tools and an actual Tool round followed by an answer. Additional failure cases:
fragmented/interleaved calls must assemble by index; incomplete or invalid calls
must never execute; intermediate commentary must not survive into the final
answer; reasoning must retain absent/empty semantics without reaching Chat;
disconnect/cancellation after a visible delta must not retry or duplicate text;
pre-output retries and unsupported stream-usage fallback must retain the frozen
schemas, sampling, request identity, usage settlement and permit release.

| Failure | Required result |
| --- | --- |
| No effective model-call/token limit and no enclosing deadline | Refuse the Tool loop before executing a Binding; normal direct answers still work. |
| Valid JSON violates a Tool schema, missing Tool, empty result, read timeout | Return the existing typed observation; the model may correct it within the same budget. |
| Incomplete generation or malformed Tool-call protocol/JSON | Stop without executing any call from that batch. |
| Reused provider Call IDs across rounds | Independent ledger/event identities and correctly paired messages. |
| Duplicate provider Call IDs within one batch | Reject the batch before any side effect. |
| Model-call, token, Tool-call or active-runtime exhaustion | Existing typed budget failure; do not invent a successful final answer. |
| Repeated no-progress results | Existing Progress Guard eventually halts the Turn. |
| Cancellation, provider error or empty final answer | Stop with an error; never synthesize `Tool execution completed.`. |
| Required context exceeds the window | Persist the failed assembly diagnostic and stop before another model request. |
| Thinking provider returns reasoning with Tool calls | Retain the exact field across all same-Turn rounds, including empty strings; keep it out of Chat and ordinary events. |
| Required reasoning exceeds the context budget | Stop before the next model request rather than drop or truncate protocol state. |
| Optional history exceeds its budget | Clip optional history without dropping or mismatching Tool call/result pairs. |
| Multiple Web searches each return local W1 | Relabel observations using the durable Run-scoped source catalog; do not collide. |
| Explicit same-Stage retry after a committed external effect | Reuse the existing Effect Journal; do not repeat the committed write or silently accept changed arguments. |
| Uncertain external effect or journal/audit failure | Stop for existing recovery/reconciliation; the model cannot simply ignore it. |

The deterministic backend integration tests use a local OpenAI-compatible HTTP
server, real Tool bindings, Context Assembly, an in-memory Fixture Store for
events/capture, and Usage Ledger. Their scope is the backend Runtime/Tool protocol,
not browser-to-API product end-to-end testing or real PostgreSQL integration.
`TestChatHTTPStreamsToolAnswerBeforeProviderDone` additionally exercises a real
HTTP `/api/chat` connection and verifies the final persisted Message after
retracting intermediate commentary. Its provider waits on a channel released
only when the HTTP consumer receives the first answer chunk; no sleeps establish
the streaming claim.
It is protocol evidence, not a live-model quality benchmark. TOOL-025 owns live
task-quality evidence; this feature does not introduce another evaluator.

## Recovery Boundary

Tool identities are derived from Run, Stage, round and batch position, not from
a provider's possibly reused identifier. Stage retry reuses those identities;
the existing journal validates the original arguments before replaying a
committed result. Changed arguments cannot silently repeat a write. An unscoped
standalone caller receives invocation-local identities, not a durable recovery
guarantee.

Single's runtime-owned `update_task_state` is a journaled internal write, not
an external effect. It uses the real Turn identity without inventing a Stage;
committed calls can replay within that Turn. Single invocation-local call IDs
still do not provide durable restart continuation. Internal-write uncertainty
stops the Turn just like external-write uncertainty. See
[Structured Task State](../runtime/task-state.md) for the version and identity
contract.

Actual orchestration Resume may abandon an unfinished Stage and create a new
Stage ID. If that Stage already has a committed external effect, checkpoint
restore refuses automatic recreation with `ErrNeedsReconciliation`. An operator
must resolve the unfinished Stage/effect boundary; replaying a completed Stage
or an explicit retry of the same Stage identity is a different case. Confirming
an external write alone does not prove its enclosing Stage completed.

Recovery remains Stage-scoped. There is no durable per-round cursor, extra
checkpoint type, Child Run or exactly-once claim. A retry may repeat model calls
and read-only work; it must not automatically repeat a confirmed external write.

## Reproduce Protocol Evidence

From `apps/api`, using the repository Go version:

```bash
go test -json ./internal/agent ./internal/agent/toolloop ./internal/checkpoint ./internal/httpapi ./internal/projection \
  -run 'TestBoundedToolLoopAcrossExecutionModes|TestMultiRound|TestRestoreDoesNotAbandonCommittedEffect|TestResumeFailurePolicyKeepsCheckpointBlockedRunRecoverable|TestBuildRecoverySummaryExplainsCommittedEffectInUnfinishedStage' \
  -count=1 -timeout=60s > /tmp/bounded-tool-loop-evidence.jsonl
```

Streaming evidence (no live model or browser):

```bash
go test -json ./internal/inference/openai ./internal/agent/toolloop ./internal/httpapi \
  -run 'TestStreamingTool|TestToolLoopStreamsBeforeProviderDone|TestChatHTTPStreamsToolAnswerBeforeProviderDone' \
  -count=1 -timeout=60s > /tmp/tool-answer-streaming-evidence.jsonl
```

The JSONL retains test/subcase identity, Run/Stage IDs, fixture model and budget,
event lifecycle, result and pass/fail outcome. Test sources define exact inputs
and local provider responses. Tests verify settled logical calls against
physical Request Capture records and unique Tool identities. This uses no live
provider, credentials, production database or quality judge. The Mode test
executes actual Single, approved Multi Worker and Autonomous Act entry points;
the recovery test distinguishes same-Stage journal replay from cross-Stage
Resume refusal. No browser automation or visual claim is included.
