# OpenTelemetry Traces

AgentFlow exports committed typed events through the Go OpenTelemetry SDK and
OTLP/HTTP to a Collector. Stored events and Replay remain authoritative; tracing
is a sampled, best-effort observation copy, not another execution journal.

## Failure Contract

| Condition | Expected behavior |
| --- | --- |
| Disabled exporter | No SDK worker or network requests |
| Invalid exporter, endpoint or sampling ratio | Reject startup without echoing credential-bearing values |
| Collector unavailable, slow or rejecting requests | Execution continues; bounded asynchronous queues may drop telemetry |
| Failed database commit | No exported event |
| Live answer/reasoning/progress deltas | Not queued or exported |
| Missing starts, duplicate boundaries or shutdown during execution | No fabricated success; unfinished spans are marked incomplete |
| Queue overflow or excessive active spans | Drop telemetry, never block a Run |
| Missing completion | Expire active spans after one hour; no indefinite accumulation |
| Waiting for user, then Resume | Close one execution segment and open a new trace with the same Run ID |
| Prompt, reasoning, arguments, results or raw error text in events | Excluded before enqueueing, regardless of Request Capture mode |

This table is the failure inventory for the focused projection, OTLP transport,
post-commit and production-composition tests.

## Run Locally

From the repository root:

```bash
docker compose -f deploy/observability/compose.yaml up -d
```

Set these in `apps/api/.env`, then restart the API:

```dotenv
OTEL_TRACES_EXPORTER=otlp
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
OTEL_SERVICE_NAME=agentflow-api
OTEL_TRACES_SAMPLER_ARG=1
```

Run a Chat task, then open [Jaeger](http://localhost:16686), choose
`agentflow-api`, and search. Use the tag `agentflow.run.id=<Run ID>` to correlate
with Replay. A Run ID is not an OpenTelemetry Trace ID. Spans are exported after
their boundary closes; batch delivery can take a few seconds. No new workbench
page or API endpoint is required.

The four settings above are the supported configuration surface. `none` (the
default) disables the observer; `otlp` selects protobuf OTLP/HTTP at `/v1/traces`.
The endpoint must be an origin without path/query/userinfo; remote endpoints
require HTTPS. Sampling is parent-based with a root ratio from 0 to 1. AgentFlow
does not pass through OTLP headers, resource attributes, sampler selection or
content-capture environment variables. Use a trusted local Collector to handle
backend authentication and onward routing.

The Compose stack is development-only: loopback host ports, resource-capped
containers, Collector memory limiter/batches/bounded sending queue, and Jaeger's
transient in-memory storage. Restarting Jaeger loses its traces. For sustained
deployment, configure authenticated ingress and durable storage/retention before
exposing either service. Only the Collector receives host OTLP traffic; Jaeger's
OTLP receiver remains inside the Compose network.

Stop it with:

```bash
docker compose -f deploy/observability/compose.yaml down
```

## Trace Contract

| Span | Source boundaries | Parent |
| --- | --- | --- |
| `agentflow.run` | `run.started`/`run.resumed` to waiting/completed/failed/canceled | New trace per active execution segment |
| `agentflow.stage` | Stage started to terminal event | Active Run segment |
| `agentflow.turn` | Turn started to terminal event | Stage, or Run for Single |
| `agentflow.model_attempt` | Request prepared to attempt finished | Turn when available; otherwise Stage/Run |
| `agentflow.tool` | Tool started to completed/failed | Turn when available; otherwise Stage/Run |

Physical retries are separate attempt spans identified by `record_id`, not one
logical model-call span. Attempt metadata includes provider/model identifiers,
attempt number, usage provenance, token counts, error kind, HTTP status, duration,
TTFT, output rate and separate admission waits when the durable event supplies
them. The span interval follows committed boundary timestamps; transport timing
fields retain their existing definitions, not estimates of inference-kernel time.

Span names are fixed, never dynamic Agent names or prompts. Attributes carry
bounded Run/Conversation/Stage/Turn/call identities, event sequence, classifications
and scalar measurements. They intentionally omit user identity, workspace names,
URL/credentials, hashes of content, editable prompts, answers, reasoning, Tool
arguments/results, and raw errors. Even `MODEL_REQUEST_CAPTURE_MODE=full` cannot
enable body export. Use policy-controlled Request Capture in Replay for content
debugging. IDs remain identifying operational metadata; restrict access and
retention at the receiving backend.

Cancellation is recorded as canceled, not a provider failure. Missing completion
on shutdown/expiration is an incomplete error span, not completed execution.
Waiting for user closes the segment so approval time does not inflate active Run
duration; Resume produces another trace searchable by the same Run ID.

## Bounds and Limitations

- One 1,024-entry sanitized boundary queue and one worker per enabled process.
- At most 2,048 active spans, expiring after one hour (checked every minute).
- SDK batch queue: 1,024 ended spans, batches of 128, one-second interval.
- HTTP export timeout: two seconds; SDK retries disabled. Shutdown flush is
  capped at three seconds independently of the caller's cancellation deadline.
- Queue/cap overflow drops observation only. The boundary drop counter is logged
  at shutdown; SDK or Collector drops are not included in that counter.

Sampling, overload, missing/out-of-order boundaries or a process crash can leave
partial traces. There is no durable export outbox, historical backfill, exactly-once
delivery or cross-restart Trace ID continuity. Start-less terminal events are
ignored; a missing parent falls back to an observed Stage/Run and is marked
`agentflow.trace.parent_boundary_missing=true`. Replay, not trace
completeness, determines actual execution outcome. Model requests without a Run
identity (such as standalone Knowledge ingestion) are not exported by this path.

This release provides traces only: no metrics/log export, frontend tracing,
provider `traceparent` propagation, SLO dashboard or alerting. The SDK provider is
application-owned and does not replace the process-global tracer provider.

## Repeatable Evidence

With the local stack running and a **dedicated test** Postgres URL with CREATEDB
permission and pgvector available:

```bash
source scripts/go-env.sh
activate_agentflow_go
TEST_DATABASE_URL='postgres://.../agentflow_test?sslmode=disable' \
TEST_OTLP_ENDPOINT=http://127.0.0.1:4318 \
TEST_JAEGER_URL=http://127.0.0.1:16686 \
GOCACHE=/private/tmp/agentflow-go-build-cache \
go -C apps/api test ./app -run '^TestProductionTelemetryCommittedExecution$' \
  -count=1 -v | tee /private/tmp/agentflow-otel-evidence.log
```

This gate creates disposable databases; it never clears the named test database.
It runs real HTTP/SSE, production composition, physical attempts and Tools in
Single/Multi/Loop; Multi includes plan approval and Resume. The provider is a
deterministic fixture, **not a live-model latency measurement**. OTLP protobuf is
decoded, compared with committed events, checked for content/credential canaries,
then forwarded to the real Collector and queried back from Jaeger's v3 API until
all committed boundaries are visible. `OTEL_EVIDENCE` lines retain Run IDs, trace
IDs, mode/status, event counts, span counts and evidence limitations. Search
`agentflow-otel-fixture` in Jaeger to inspect those traces.

Without `TEST_OTLP_ENDPOINT`, the same test verifies delivery to a local fixture
receiver, not the Collector/Jaeger chain. `TEST_JAEGER_URL` enables storage
verification and requires Collector forwarding. Failure and race checks:

```bash
GOCACHE=/private/tmp/agentflow-go-build-cache \
go -C apps/api test -race ./internal/telemetry ./app \
  -run 'Test(ExecutionHierarchy|ResumeCancellation|BoundedQueue|ActiveSpan|OTLPHTTP|InvalidConfiguration|Projection|StaleSpan|CollectorRejection|ExporterError|AttemptMetadata|ObservableStore)' \
  -count=1
```

For missing traces, check `enabled=true` at API startup, sampling ratio, completed
boundaries, `docker compose ... ps` and Collector logs. Export failure logs contain
stable summaries, never response bodies. Disabling `OTEL_TRACES_EXPORTER` and
restarting the API rolls back telemetry without a schema or execution change.

References: [Go SDK/exporters](https://opentelemetry.io/docs/languages/go/exporters/),
[Collector](https://opentelemetry.io/docs/collector/),
[Jaeger all-in-one](https://www.jaegertracing.io/docs/2.21/getting-started/).
