# Completion Verification

AgentFlow can treat an assistant response as a candidate result until a frozen
Completion Contract is satisfied. Verification is explicitly enabled per Run;
ordinary chat does not require it and keeps
`verification_status=not_required`.

This subsystem answers whether configured outcome invariants passed. It is
separate from execution success, provider retries, retrieval relevance, and
subjective factual correctness.

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

The chat composer exposes this opt-in under **Completion verification**. Its verifier tabs can configure all five built-in types: text constraints, citation policy, JSON Schema, read-only HTTP checks, and allowlisted commands. The same policy applies to the next `single`, `multi_agent`, or `autonomous` Run because all three modes share the same chat request path. HTTP checks still follow the backend host allowlist; command checks require `VERIFICATION_WORKSPACE_ROOT` and an allowlisted executable. The final SSE `done` event includes `verification_status`, and the workspace shows that status separately from the Run lifecycle status. Disabling the control omits `completion_contract` entirely.

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
- `text_constraints`: checks character/word bounds, required or forbidden phrases, and required Markdown headings.
- `citation`: checks explicit Markdown citation count, HTTPS use, and allowed or blocked source hosts.

Each Evidence record binds the contract/version, verifier/version, Runtime Snapshot hash, exact candidate Subject Hash, attempt number, status, duration, exit code, summary, structured details, and Artifact IDs. One verifier may emit multiple bounded Artifacts, such as a score report and diagnostics. Evidence is append-only. When a later candidate has a different Subject Hash, AgentFlow appends a `stale` marker that references the superseded Evidence rather than rewriting history.

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

- Deterministic outcome checks: command/test execution, HTTP probes, JSON Schema, database assertions, file or state invariants.
- Deterministic response checks: text constraints, citation/source policy, parsers, static analyzers, and exact-match rules.
- Model-based graders: rubric scoring, groundedness, completeness, and pairwise comparison can return scores and claim-level diagnostics through `details` and Artifacts.
- Human review: expert judgment and calibration should enter through a future asynchronous evidence-ingestion path rather than blocking a synchronous verifier process.

The Completion Gate deliberately consumes only `passed`, `failed`, or `blocked`; verifier-specific scores remain evidence details. Thresholds and rubric weighting belong inside the verifier that owns their semantics. The gate policy stays limited to required checks, `all_must_pass`/`any_may_pass`, attempt bounds, and exhaustion behavior.

For subjective outputs such as articles and research reports, combine deterministic checks with an appropriately calibrated model or human grader. Deterministic structure and citation checks are useful evidence, but they are not substitutes for factuality, source quality, argument coherence, or editorial judgment.

## Security Boundaries

Command verification is disabled unless `VERIFICATION_WORKSPACE_ROOT` and `VERIFICATION_ALLOWED_COMMANDS` are both configured. Working directories must be relative and remain below the root. HTTP verification permits loopback targets by default; other exact hosts require `VERIFICATION_ALLOWED_HTTP_HOSTS`. Redirects are checked against the same policy. HTTP verifiers cannot send mutation methods or persisted authorization headers.

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

This MVP intentionally omits a generic CI runner, asynchronous human evidence ingestion, and built-in model graders. Deployments that later register model graders should retain deterministic or human checks for high-impact completion decisions rather than relying on an LLM judge alone.
