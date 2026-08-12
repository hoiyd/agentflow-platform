# Failure Handling

AgentFlow preserves subsystem-specific error types while projecting failures
through one transport-neutral contract. This keeps recovery policy close to the
owner and gives Run Events, Replay, and Episode Reports a stable vocabulary.

## Common Contract

Errors that cross a runtime boundary may implement `failure.Classified` and
return `failure.Info`:

| Field | Meaning |
| --- | --- |
| `code` | Stable, specific failure kind such as `rate_limited`, `tool_not_found`, or `budget_exceeded`. |
| `source` | Owning boundary such as `model_provider`, `tool`, `budget`, or `verification`. |
| `category` | Broad operational class: `canceled`, `timeout`, `availability`, `authentication`, `quota`, `validation`, `not_found`, `capacity`, `execution`, or `internal`. |
| `retryable` | Whether the owning subsystem considers retry potentially useful. This is evidence, not permission to bypass Run limits. |
| `operation` | Optional operation or reservation identifier. |
| `details` | Bounded, non-secret diagnostic values owned by the subsystem. |

The shared package depends only on the Go standard library. Model, Tool,
Budget, Store, Context Assembly, request limiting, run admission, embedding,
and Verification errors retain their existing concrete types and implement the
contract without importing one another.

Unknown errors are classified as `unclassified / application / internal`.
`context.Canceled` and `context.DeadlineExceeded` receive deterministic
canceled and timeout classifications even when no subsystem wrapper exists.

## Trace Projection

Failure events keep the existing human-readable `error` value and add:

```json
{
  "error": "model request failed: ...",
  "error_kind": "rate_limited",
  "error_source": "model_provider",
  "error_category": "availability",
  "retryable": true
}
```

Provider-safe details such as status code, retry delay, and attempt count may
also be present. Raw provider bodies, credentials, prompts, and secrets must
not be placed in structured failure details.

Episode Report projects these fields into each trace-derived error as `kind`,
`source`, `category`, and `retryable`. Legacy persisted events remain readable;
their new fields are simply empty.

## Verification Blocked Reasons

`blocked` means a verifier could not produce a pass/fail judgment. Evidence
therefore includes a stable `details.reason_code`, for example
`config_invalid`, `policy_denied`, `canceled`, `timed_out`, `unavailable`,
`embedding_failed`, or `implementation_unavailable`. Human-readable summaries
remain explanatory text and must not be parsed for control flow.

## Compatibility Boundary

This contract is additive. Existing error strings, concrete Go error types,
Run statuses, and the HTTP error envelope `{"error":"..."}` are unchanged.
Transport-level typed error responses can be versioned separately if the API
later needs machine-readable errors outside Replay and Episode Report.
