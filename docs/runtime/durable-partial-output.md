# Durable Partial Output Recovery

Partial output is display evidence, not a final answer, a Stage execution
checkpoint, or provider continuation state. Recovery must not issue another
model request or replay a Tool as a side effect of reading output.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Refresh, disconnect or slow observer | Restore committed text and its durable cursor, then replace with newer checkpoints without appending duplicates |
| Tool-round answer Reset | Persist retraction before accepting subsequent text; old commentary cannot reappear |
| New model call, attempt or resumed Turn | Separate identities; never concatenate old and new answers |
| Failure, cancellation or process crash | Show only the last committed display copy, clearly incomplete; do not infer success or automatic execution recovery |
| Persistence failure | Surface a failure, do not publish an uncommitted checkpoint or claim durable recovery |
| Recovery read failure | Keep existing Run/Trace information and show a recovery diagnostic |
| Large output / many deltas | Bound retained UTF-8 text and checkpoint frequency; do not write once per token |
| Credential split across deltas | Redact a whole bounded display prefix, not each fragment independently; never store private continuation |
| Workspace revocation or deletion | Reuse Run authorization, SSE rechecks and cascading Run-event deletion |

## Commit and Recovery Protocol

The shared Turn Engine wraps its existing event Sink with an OutputRecorder.
It commits `model.output_checkpoint` facts to the existing Run Events table:
the first safe prefix, pending changes every **1 second**, changes of at least
**16 KiB**, and explicit Reset/terminal boundaries. Unchanged display and metadata
do not produce writes. These are full display replacements, not token-level inserts.
The initiating browser still receives immediate answer/reasoning updates;
reconnected observers receive committed replacements, at checkpoint cadence.

Each checkpoint carries Run, optional real Stage, Turn, model call/attempt when
available, logical round, revision, UTF-8 byte offset, channel, and status.
`offset` is the byte length of the stored display copy, **not** a provider token
offset. Event sequence orders commits across channels; revision orders one
output slot. A Reset commits an empty `retracted` replacement. New calls and
attempts never append to another call's answer; resumed Turns have new identity.

`GET /api/runs/{id}/projection` and Replay expose `partial_outputs`. The existing
Snapshot-and-subscribe Hub boundary closes the read/subscribe race. Reconnect
uses its durable event cursor, ignores duplicate sequences, and replaces text
instead of appending a checkpoint to prior deltas. The workbench loads this view
on conversation reload and displays **Provisional** or **Incomplete** output.
Worker/Loop drafts retain their Stage identity and do not masquerade as the
final assistant answer. A completed Run returns no partial display; its ordinary
assistant Message remains the sole final answer.

## Limits and Failure Semantics

- Answer display: **64 KiB** per slot; enabled reasoning display: **16 KiB** per
  call. Recovery retains the latest **32** display slots. UTF-8 boundaries and
  an explicit truncation flag are preserved. Event retention is the existing Run
  lifecycle, not a new TTL or storage service.
- The OpenAI-compatible adapter sanitizes cumulative display copies, including
  its configured API key; required continuation and returned protocol text stay
  unchanged. Unfinished tokens/key lookahead can delay a safe prefix beyond the
  checkpoint interval. Reasoning still requires an explicitly supported format.
  Existing pattern redaction is not comprehensive PII detection.
- With healthy storage, pending safe updates are considered each second or at
  the byte threshold. This is **not a hard one-second durability SLA**: storage
  latency and safe-prefix filtering also apply. SIGKILL loses everything after
  the last successful commit. Reading a saved prefix cannot resume an in-flight
  provider request, KV state, or unknown external side effect.
- Failure/cancellation/recoverable crash preserves the last committed prefix
  and marks it incomplete. Pre-Resume provisional text stays interrupted, never
  reappears as an active attempt. A final call display is not proof that the
  whole Run passed its budget or completion gate.
- Checkpoint write errors cancel model work and surface an execution failure;
  the Hub publishes only committed facts. A storage outage can also prevent the
  final diagnostic from being saved: the current request reports failure, and
  existing heartbeat/recovery behavior applies after restart.
- No schema migration is needed. Scoped Run reads and SSE authorization rechecks
  remain authoritative. Conversation deletion cascades to checkpoint events.
  Old live-only prefixes cannot be recovered retroactively.

No Tool-progress recovery, steering inbox, automatic worker takeover, or second
event store is introduced. Stage execution recovery remains governed by
[Durable recovery](durable-recovery.md).

## Repeatable Evidence

```bash
# Use only a disposable test database/server with CREATEDB privileges.
TEST_DATABASE_URL='postgres://<user>@127.0.0.1:5432/<dedicated-test-db>?sslmode=disable' \
AGENTFLOW_REASONING_TEST=1 bash scripts/test-browser.sh partial-output.spec.ts

cd apps/api
go test ./internal/event ./internal/eventcatalog ./internal/agent/turn ./internal/projection
TEST_DATABASE_URL='<dedicated-test-database-url>' \
go test ./internal/store -run TestPostgresPartialOutputSurvivesWorkerKill -count=1 -v
```

The browser gate exercises Single/Multi/Loop through production composition and
disposable Postgres: Reset, refresh, no extra provider request, cancellation,
second reload, and canonical finalization. JSON attachments under
`apps/web/test-results/` retain run/sequence identities, committed projection,
request counts and fixture limitations. The Postgres test actually kills a
worker subprocess before/after its checkpoint, reopens the Store, checks scoped
reads and cascading deletion. Fixtures are compatibility evidence, not live
provider or multi-worker takeover measurements.
