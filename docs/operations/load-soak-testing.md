# Bounded Load and Soak Testing

PROD-015 provides a repeatable, network-free load profile for AgentFlow's
existing process-local controls. It exercises production controllers and
adapters against controlled Provider, Tool, and Memory dependencies; it does
not estimate real-model capacity.

## Run

From the repository root:

```bash
make load-evidence
```

The default two-second soak keeps local and CI runs bounded. Use a longer,
still-bounded duration when collecting manual evidence:

```bash
SOAK_DURATION=30s make load-evidence
```

`SOAK_DURATION` must be between `250ms` and `5m`. Reports are written to
`.cache/load-soak` by default; `LOAD_REPORT_DIR` selects another directory.

## Fixed Profile

| Input | Default |
| --- | ---: |
| Active Runs | 2 |
| Run queue | 2 |
| Queue wait timeout | 250 ms |
| Model requests in flight | 1 |
| Provider latency | 20 ms |
| Long-tail request | Every fifth request at 4x latency |
| Tool latency | 5 ms |
| Memory sync queue | 2 |
| Soak arrival interval | 5 ms |

The JSON report records this configuration, its SHA-256 identity, Git revision,
Go version, platform, CPU count, timestamps, and sample counts. Compare reports
only when their configuration and environment are equivalent.

## Load Curve

The same harness runs five segments:

| Segment | Purpose |
| --- | --- |
| `underloaded` | Requests arrive below service capacity; all must complete without rejection. |
| `saturated` | A bounded burst shares one Conversation; queue latency rises while single-writer ownership remains one. |
| `overloaded` | Offered work exceeds active plus queued capacity; excess work must fail with `run_queue_full`. |
| `recovery` | A later low-rate segment must complete after overload without stale permits. |
| `soak` | Fixed-rate arrivals continue for the configured duration to expose leaks or stuck capacity. |

Each segment records offered, accepted, rejected, completed, and failed counts;
queue and service p50/p95 latency; failure codes; and peak active Runs. The
accounting identities are enforced:

```text
offered = accepted + rejected
accepted = completed + failed
```

## Boundaries Exercised

- `RunController`: bounded admission, queueing, per-Conversation single writer,
  overload rejection, drain, and post-overload recovery.
- `ModelRequestLimiter`: global HTTP request semaphore plus explicit RPM wait
  cancellation, refill recovery, and TPM capacity rejection.
- `budget.Tracker`: real reservation and settlement through the fixture Store;
  an oversized Run is rejected as `budget_exceeded` before provider work.
- `tools.Executor`: a normal bounded Tool call runs inside each accepted sample;
  a slow Tool must return `execution_timeout`.
- `memory.BuiltinProvider`: post-response sync remains non-blocking; a full
  bounded queue reports accepted and rejected jobs without changing Run results.
- OpenAI-compatible transport: controlled latency, long-tail responses, provider
  `429`, timeout, and a handler that intentionally ignores cancellation.

After faults, the suite requires a successful request, drained Run and Memory
workers, zero fixture connections, bounded goroutine growth, and no concurrency
limit violation. Heap allocation before and after cleanup is reported but not
used as a pass/fail threshold because the Go runtime retains reusable memory.

## Evidence

The command produces:

- `bounded-load.json`: complete machine-readable identity, metrics, controls,
  resource observations, and limitations;
- `bounded-load.md`: compact saturation table for PRs and interview review.

Default backend CI already sets `EVALUATION_REPORT_DIR`, so `go test ./...`
retains these files with the other offline evaluation artifacts. No public
model, OTel collector, worker service, or external load-test framework is
required.

## Interpretation Boundary

The report proves that local admission, cancellation, accounting, and cleanup
honor their configured bounds under controlled load. Stub throughput is not a
real LLM QPS measurement, capacity plan, latency SLO, or multi-instance result.
Use a provider-approved staging profile and equivalent configuration before
making those claims.
