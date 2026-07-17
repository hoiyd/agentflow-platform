# Completion Verification

AgentFlow treats an assistant response as a candidate result until a frozen Completion Contract is satisfied. Verification is explicitly enabled per Run; ordinary chat does not require verification and keeps `verification_status=not_required`.

## When Verification Runs

Verification is **opt-in**, not inferred from the chat mode or server configuration.

A Run enters the verification lifecycle only when its initial `POST /api/chat` request includes a valid, non-null `completion_contract`. The server validates and freezes that contract before creating the Run. This rule is identical for `single`, `multi_agent`, and `autonomous` modes.

| Situation | Verification behavior |
|---|---|
| `POST /api/chat` omits `completion_contract` | Run starts as `not_required`; it completes through the normal path without verifiers or Evidence. |
| `POST /api/chat` includes a valid `completion_contract` | Run starts as `pending`; the Completion Gate runs when the Run produces a candidate final output. |
| Multi-Agent or Autonomous Run pauses at an intermediate `waiting_for_user` boundary | The Gate does not run yet because no completion is being claimed. The frozen contract remains attached to the Run. |
| A contracted Run is continued or resumed and reaches the completion path | The inherited frozen contract is evaluated; callers cannot replace or weaken it. |
| `POST /api/runs/{id}/verify` is called | An existing contracted Run consumes another bounded verification attempt against its latest persisted assistant output. |
| `/verify` is called for an ordinary Run | The API returns `409`; it does not retrofit a contract onto an existing Run. |

`VERIFICATION_WORKSPACE_ROOT`, command allowlists, HTTP host allowlists, and Artifact limits only configure which verifier implementations may run safely. Setting these environment variables does **not** enable verification for any Run by itself.

## Execution Model

```text
create Run + freeze contract
  -> execute turns/stages
  -> persist candidate assistant output
  -> compute subject hash
  -> run deterministic verifiers
  -> persist immutable evidence + bounded raw-output artifacts
  -> evaluate completion policy
  -> publish run.completed only when the gate passes
```

The first implementation supports:

- `command`: executes an argument vector directly, without a shell.
- `http`: performs read-only `GET` or `HEAD` checks.
- `json_schema`: validates the final Run output with JSON Schema 2020-12.

Each Evidence record binds the contract/version, verifier/version, Runtime Snapshot hash, exact candidate Subject Hash, attempt number, status, duration, exit code, summary, and Artifact IDs. Evidence is append-only. When a later candidate has a different Subject Hash, AgentFlow appends a `stale` marker that references the superseded Evidence rather than rewriting history.

## Policy Semantics

`required=true` means the verifier participates in the Completion Gate. Optional verifiers still run and produce Evidence, but cannot block completion.

- `all_must_pass`: every required verifier must pass.
- `any_may_pass`: at least one required verifier must pass.
- `max_attempts`: bounds verification/revision attempts from 1 to 5.
- `on_exhausted=fail`: closes the Run as `failed`.
- `on_exhausted=waiting_for_user`: closes the attempt as `waiting_for_user`.

A failed or blocked check remains `failed_recoverable` while attempt budget remains. `POST /api/runs/{id}/verify` creates a new attempt against the latest persisted assistant output. A verifier that cannot run is `blocked`, never implicitly passed.

## Contract Example

Opt one new Run into verification by passing the contract with `POST /api/chat`:

```json
{
  "message": "Return the service health as JSON.",
  "completion_contract": {
    "subject_type": "run_output",
    "verifiers": [
      {
        "id": "response-schema",
        "type": "json_schema",
        "required": true,
        "json_schema": {
          "schema": {
            "type": "object",
            "properties": {
              "status": { "const": "ok" }
            },
            "required": ["status"],
            "additionalProperties": false
          }
        }
      },
      {
        "id": "service-health",
        "type": "http",
        "required": true,
        "http": {
          "method": "GET",
          "url": "http://localhost:8080/health",
          "expected_status": 200
        }
      }
    ],
    "policy": {
      "mode": "all_must_pass",
      "max_attempts": 2,
      "on_exhausted": "waiting_for_user"
    }
  }
}
```

The server assigns the contract ID when omitted, freezes verifier implementation versions and defaults, and stores a canonical contract hash before the Run starts. Request-provided `hash` and `version` values are not trusted.

## Security Boundaries

Command verification is disabled unless `VERIFICATION_WORKSPACE_ROOT` and `VERIFICATION_ALLOWED_COMMANDS` are both configured. Working directories must be relative and remain below the root. HTTP verification permits loopback targets by default; other exact hosts require `VERIFICATION_ALLOWED_HTTP_HOSTS`. Redirects are checked against the same policy. HTTP verifiers cannot send mutation methods or persisted authorization headers.

Verifier output is hashed and persisted as an Artifact, capped by `VERIFICATION_MAX_ARTIFACT_BYTES` (64 KiB by default). The Artifact records the observed byte count and whether stored content was truncated.

## Replay and Events

`GET /api/runs/{id}/replay` returns `verification_evidence` and `verification_artifacts`. `GET /api/runs/{id}/episode` adds a verification summary while retaining the raw records.

Lifecycle events are:

```text
verification.requested
verification.started
verification.passed
verification.failed
verification.blocked
verification.stale
run.revision_requested
```

File and Postgres stores use the same domain contract. Postgres creates the verification tables and Run columns through idempotent startup migration.

## Design Basis

The implementation follows three practical evaluation principles:

- Make capabilities and invariants mechanically legible to the agent and runtime, as described in [OpenAI Harness Engineering](https://openai.com/index/harness-engineering/).
- Grade the real outcome separately from the agent transcript, and prefer deterministic code-based graders where possible, as described in [Anthropic's evaluation guide](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents).
- Give the loop an explicit verification phase, stopping rule, named terminal outcome, and bounded attempt budget, consistent with [recent loop-engineering specification work](https://arxiv.org/abs/2607.00038).

This MVP intentionally omits a generic CI runner and does not permit an LLM judge to be the only required verifier.
