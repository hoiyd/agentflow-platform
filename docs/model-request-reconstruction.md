# Model Request Reconstruction and Context Capture

AgentFlow persists one `ModelRequestEnvelope` before every run-scoped physical
chat request attempt. The envelope records the logical Model Call, physical
attempt number, provider/model, effective non-content parameters, Context
Manifest reference, Runtime Snapshot hash, exact transport payload hash,
message/tool counts, and selected token totals by Context source. It never
contains request headers or credentials.

This separates three related contracts:

| Contract | Responsibility |
| --- | --- |
| Runtime Snapshot | Freezes Run-level model, Agent, Tool, Context, and Budget protocol. |
| Context Manifest | Explains which sources were selected, excluded, or transformed. |
| Model Request Envelope | Identifies the final effective payload for one physical attempt. |
| Context Capture | Optionally retains a controlled copy of that payload for debugging. |

## Capture Modes

```bash
MODEL_REQUEST_CAPTURE_MODE=metadata_only
MODEL_REQUEST_CAPTURE_MAX_BYTES=262144
MODEL_REQUEST_CAPTURE_RETENTION=168h
```

- `metadata_only` is the default. It stores the payload hash and structural
  metadata without prompt content.
- `redacted` recursively removes sensitive JSON fields and common credential
  patterns before storing bounded canonical JSON. Its strategy and replacement
  count are recorded. Redacted captures are useful for debugging but are not
  exact reconstructions.
- `full` stores the exact canonical JSON sent to the transport when it fits the
  configured limit. It is the only mode that may set `reconstructable=true` and
  should be enabled only in a trusted local environment.

When redacted or full content exceeds the limit, AgentFlow retains the envelope
and marks the capture truncated without persisting a partial JSON fragment.
Stored content receives an expiry. File and Postgres stores lazily purge expired
content on read while retaining the Envelope, payload hash, expiry, and redaction
metadata for long-term audit.
API keys and Authorization headers never enter the recorder because transport
headers are applied after the payload observation boundary.

## Logical Calls and Physical Attempts

A provider retry reuses one `model_call_id` and receives the next `attempt`
number. The Run Budget counts the logical call once, while each physical
attempt has its own envelope and `model.request_prepared` event. A stream usage
capability fallback also creates another attempt because its effective payload
differs from the original request.

The recorder runs before the provider request. A run-scoped persistence failure
fails closed rather than sending an unrecorded request. File and Postgres stores
assign attempt numbers atomically per `(run_id, model_call_id)`.

## Inspection API

```http
GET /api/runs/{id}/model_requests
GET /api/runs/{id}/model_requests?include_content=true
```

The default response omits Capture content while returning Envelope metadata,
Capture status, the referenced Context Manifest, and a source diff. The diff
compares selected token totals frozen in the Envelope with selected/excluded
totals derived from the Manifest. Sources include system, current input,
history, compaction summary, memory, knowledge/RAG, Tool definitions, and Tool
results when present. `include_content=true` explicitly requests unexpired
stored content and still observes Workspace isolation.

The response also reports `reconstructability_status`. The invariant checks:

- Runtime Snapshot hash equality;
- contiguous physical attempt numbers;
- matching `model.request_prepared` records and events;
- referenced Context Manifest existence;
- source token breakdown equality between Envelope and Manifest;
- exact payload hash equality for reconstructable full captures.

Authorization beyond Workspace isolation remains a deployment responsibility
until AgentFlow adds an authenticated identity and policy layer. Automatic
failure-only capture escalation is also intentionally deferred: metadata-only
requests cannot be upgraded after the original payload has been discarded.
