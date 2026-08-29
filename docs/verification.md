# Verification

AgentFlow can treat an assistant response as a candidate result until a frozen
Completion Contract is satisfied. Verification is explicitly enabled per Run;
ordinary chat does not require it and keeps
`verification_status=not_required`.

> **Verification is not test execution.** Verification evaluates one Run's
> persisted candidate output against its frozen contract before that Run may be
> reported as completed. Automated and Manual Tests evaluate system behavior.

This subsystem answers whether configured outcome invariants passed. It is
separate from execution success, provider retries, retrieval relevance, and
subjective factual correctness.

## Verification vs. Tests

| Verification | Automated Tests | Manual Tests |
| --- | --- | --- |
| Runs inside the product for one opted-in Run. | Run during development or CI. | Are performed by a developer, operator, or reviewer. |
| Evaluates one persisted candidate output against a frozen Completion Contract. | Evaluate code against automated assertions. | Exercise a documented workflow and inspect its observable result. |
| Produces `verification.*` events and Evidence/Artifacts that can gate `run.completed`. | Produce test output and determine build health. | Produce human observations and never gate `run.completed`. |
| Is enabled by `completion_contract`; ordinary Runs skip it. | Start with commands such as `go test` or `make test`. | Follow [Manual Tests](manual-tests.md). |

A command verifier may invoke an allowlisted external test runner as one source
of runtime evidence. AgentFlow does not discover or run its own repository test
suite as part of Verification unless an operator explicitly
allowlists and configures such a command.

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

The chat composer exposes this opt-in under **Verification** and
supports all five built-in verifier types. The request behavior is identical
for `single`, `multi_agent`, and `autonomous` Runs.

HTTP checks follow the backend host allowlist. Command checks also require
`VERIFICATION_WORKSPACE_ROOT` and an allowlisted executable. The terminal SSE
`done` event reports `verification_status` separately from Run status.
Disabling the control omits `completion_contract`.

Run Verification, Evidence, Artifacts, Replay, and Episode access are guarded by
the resolved Run Workspace. An omitted request scope selects
`default_workspace`; a Run ID from another namespace is treated as not found.
This namespace check does not authenticate the caller.

## Execution Model

```text
create Run + freeze contract
  -> execute turns/stages
  -> persist candidate assistant output
  -> compute subject hash
  -> run configured verifiers
  -> persist immutable evidence + bounded raw-output artifacts
  -> evaluate completion policy
  -> publish run.completed only when the gate passes
```

The current implementation supports:

- `command`: executes an argument vector directly, without a shell.
- `http`: performs read-only `GET` or `HEAD` checks.
- `json_schema`: validates the final Run output with JSON Schema 2020-12.
- `text_constraints`: checks character/word bounds, required or forbidden phrases, and required Markdown headings.
- `citation`: checks explicit Markdown citation count, HTTPS use, and allowed or blocked source hosts.

Each Evidence record binds the contract/version, verifier/version, Runtime Snapshot hash, exact candidate Subject Hash, attempt number, status, duration, exit code, summary, structured details, and Artifact IDs. For question-aware checks, the Subject Hash binds both the user question and candidate answer. One verifier may emit multiple bounded Artifacts, such as a score report and diagnostics. Evidence is append-only. When a later candidate has a different Subject Hash, AgentFlow appends a `stale` marker that references the superseded Evidence rather than rewriting history.

## Policy Semantics

`required=true` means the verifier participates in the Completion Gate. Optional verifiers still run and produce Evidence, but cannot block completion.

- `all_must_pass`: every required verifier must pass.
- `any_may_pass`: at least one required verifier must pass.
- `max_attempts`: bounds verification/revision attempts from 1 to 5.
- `on_exhausted=fail`: closes the Run as `failed`.
- `on_exhausted=waiting_for_user`: closes the attempt as `waiting_for_user`.

A failed or blocked check remains `failed_recoverable` while attempt budget remains. `POST /api/runs/{id}/verify` creates a new attempt against the latest persisted assistant output. A verifier that cannot run is `blocked`, never implicitly passed. Every blocked Evidence record includes a stable `details.reason_code`; callers use that code rather than parsing its human-readable summary.

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
        "config": {
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
        "config": {
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

All verifier-specific settings live under `config`. Each registered verifier strictly decodes its own config, rejects unknown fields, applies defaults, and freezes the normalized value into the Completion Contract. For example, a research-style response can use deterministic checks without pretending they prove factual correctness:

```json
{
  "subject_type": "run_output",
  "verifiers": [
    {
      "id": "report-shape",
      "type": "text_constraints",
      "required": true,
      "config": {
        "min_words": 300,
        "max_words": 1200,
        "required_headings": ["Findings", "Sources"],
        "forbidden_phrases": ["TODO"]
      }
    },
    {
      "id": "source-policy",
      "type": "citation",
      "required": true,
      "config": {
        "min_citations": 3,
        "min_unique_hosts": 2,
        "require_https": true,
        "blocked_hosts": ["example.invalid"]
      }
    }
  ],
  "policy": {
    "mode": "all_must_pass",
    "max_attempts": 2,
    "on_exhausted": "waiting_for_user"
  }
}
```

`citation` counts unique external URLs from explicit Markdown links and CommonMark autolinks. Relative links, images, and bare URL-like text are not citations. Host rules match the configured host and its subdomains. This verifier does not fetch sources or judge whether a source supports a claim; source reachability and claim groundedness belong in separate verifiers.

## Extension Model

The execution interface is intentionally small: a verifier declares a stable type and implementation version, owns config normalization, and returns a status, compact structured details, and zero or more artifacts. `Registry.Register` adds an implementation before the Engine starts serving Runs. A new synchronous verifier does not require changes to `VerifierSpec`, Completion Contract hashing, gate evaluation, persistence, replay, or event schemas.

This supports the common verifier families without forcing them into one scoring method:

- Deterministic outcome checks: allowlisted commands, HTTP probes, JSON Schema,
  database assertions, and file or state invariants.
- Deterministic response checks: text constraints, citation/source policy, parsers, static analyzers, and exact-match rules.
- Model-based graders: rubric scoring, groundedness, completeness, and pairwise comparison can return scores and claim-level diagnostics through `details` and Artifacts.
- Human review: expert judgment and calibration should enter through a future asynchronous evidence-ingestion path rather than blocking a synchronous verifier process.

The Completion Gate deliberately consumes only `passed`, `failed`, or `blocked`; verifier-specific scores remain evidence details. Thresholds and rubric weighting belong inside the verifier that owns their semantics. The gate policy stays limited to required checks, `all_must_pass`/`any_may_pass`, attempt bounds, and exhaustion behavior.

`answer_relevance` remains a recognized historical type so Replay can decode
previous Contracts and Evidence. It is no longer registered by default because
uncalibrated embedding similarity is not a reliable completion criterion. A
new Contract that requests it is rejected as unsupported. Re-running a frozen
historical Contract records `blocked` Evidence with
`reason_code=implementation_unavailable`; persisted Replay remains readable.

For subjective outputs such as articles and research reports, combine deterministic checks with an appropriately calibrated model or human grader. Deterministic structure and citation checks are useful evidence, but they are not substitutes for factuality, source quality, argument coherence, or editorial judgment.

## Security Boundaries

The command verifier is disabled unless `VERIFICATION_WORKSPACE_ROOT` and
`VERIFICATION_ALLOWED_COMMANDS` are both configured. Working directories must
be relative and remain below the root. The HTTP verifier permits loopback
targets by default; other exact hosts require
`VERIFICATION_ALLOWED_HTTP_HOSTS`. Redirects follow the same policy. HTTP
verifiers cannot send mutation methods or persisted authorization headers.

Each verifier Artifact and the structured Evidence details are capped by `VERIFICATION_MAX_ARTIFACT_BYTES` (64 KiB by default). At most eight Artifacts are retained per Evidence record. Artifacts record the observed byte count, content hash, media type, and whether stored content was truncated.

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

## Design Basis and Limits

The implementation follows three practical evaluation principles:

- Make capabilities and invariants mechanically legible to the agent and runtime, as described in [OpenAI Harness Engineering](https://openai.com/index/harness-engineering/).
- Grade the real outcome separately from the agent transcript, and prefer deterministic code-based graders where possible, as described in [Anthropic's evaluation guide](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents).
- Give the loop an explicit verification phase, stopping rule, named terminal outcome, and bounded attempt budget, consistent with [recent loop-engineering specification work](https://arxiv.org/abs/2607.00038).

Current boundaries exclude a generic CI runner, asynchronous human evidence
ingestion, and built-in model graders. Deployments that register model graders
should retain deterministic or human checks for high-impact completion
decisions rather than relying on an LLM judge alone.
