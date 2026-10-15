# Structured Output Contract

An optional `completion_contract.output` connects final-answer generation to one
existing required JSON Schema verifier. Ordinary Chat and verification-only
contracts do not change. The example consumer is a material-extraction report
with `summary` and `facts`: [request example](../../examples/structured-output/report-request.json).

## Three Distinct Guarantees

| Setting | Generation | Acceptance |
| --- | --- | --- |
| No `output` | Existing prompts; optional Schema verification only | Existing Completion Gate |
| `output.mode=local` | Schema guidance in final-answer Context; no provider constraint | Complete JSON object and all required verifiers must pass |
| `output.mode=strict` | Same guidance plus an explicitly supported provider wire constraint | The same local validation and Completion Gate; provider claims are not evidence of success |

`output.verifier_id` references the schema in `verifiers[].config.schema`; do not
copy the schema into another configuration. Freeze assigns
`output.version=json-schema-output-v1` and includes it in the existing contract
hash. Structured output requires `all_must_pass` and a required JSON Schema
verifier. Grounding/relevance checks remain independent: valid JSON is not proof
that its claims are true.

In Verification > JSON Schema, **Output generation** selects these settings.
Replay shows the result only when the output contract was requested, with the
verification status and an expandable JSON object when consumable.

## Provider and Schema Boundaries

Strict generation requires both route capabilities:

```json
{"structured_output":true,"structured_output_format":"openai_json_schema"}
```

The OpenAI-compatible adapter sends `response_format.type=json_schema`, with
`json_schema.strict=true`. Set the format only for a provider/model known to
support that Chat Completions wire protocol, including streaming and Tools when
used. The boolean alone is insufficient. Unsupported routes fail before HTTP;
provider rejection stays a typed model failure, never silently falls back to
local mode. See the [official protocol](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=chat).

Local validation uses JSON Schema 2020-12 (the default when `$schema` is omitted);
other declared dialects and remote schema loading are rejected. `format` is
annotation-only, not a format assertion.
Output roots must be objects; schemas are limited to 16 KiB. Strict mode accepts
a deliberately smaller subset: concrete `type`, `properties`, `required`,
`additionalProperties`, `items`, `enum`, `description`, `title`, at most ten
nested levels. Objects require every property and `additionalProperties:false`;
arrays require `items`. References, unions, nullable type arrays, `format`,
numeric/string bounds and other keywords require explicit local mode. No schema
is silently weakened to satisfy a provider.

Schema guidance and strict wire-schema tokens both count toward assembled Context
limits and route capacity estimates. Contracted Runs assemble against the smaller
of their frozen Context window and selected frozen route window. Request metadata
retains the schema hash, not private descriptions or
examples; exact request content follows the existing opt-in capture policy.

## Execution and Recovery

- Single applies the contract to its Tool rounds and final answer. Tools still
  have their own argument/result contracts; observations are not report objects.
- Multi-Agent constrains only Finalizer; Planner, Router, Worker and Reviewer
  keep their existing protocols.
- Bounded Loop constrains a tool-free, checkpointed Final Stage after Decide.
  This is one normal budgeted generation, not an automatic format-repair loop.
- Resume uses the persisted Run's frozen contract and route descriptor, never a
  new request schema. Completed Stages remain reusable. Internal decisions,
  compaction, titles and Memory extraction do not inherit the report schema.

SSE text is provisional. Refusal/content filtering, cutoff, incomplete streams
and unconfirmed finish reasons cannot yield a machine result. Existing typed
model errors remain in SSE/Run events; Schema evidence distinguishes
`invalid_json`, `schema_mismatch`, `output_too_large`, and `schema_valid`.
Structured output accepts at most 64 KiB of candidate text; verification-only
contracts retain their existing behavior. No answer repair,
automatic retry of a Run, or replay of side effects is added.

## Machine Consumption

Submit the example to `POST /api/chat`, adding the owned `agent_id` and existing
Workspace/session authentication. After terminal SSE, use its `result_url` or:

```bash
curl --fail-with-body --cookie '<session-cookie-file>' \
  -H 'X-Workspace-ID: <owned-workspace-id>' \
  'http://localhost:8080/api/runs/<run-id>/result' | jq -e '.value'
```

| HTTP | Contract |
| --- | --- |
| `200` | `status=completed`, `verification_status=passed`, verified `value`, contract hash and evidence/Artifact IDs |
| `409` | Same status envelope with `unavailable_reason`, **without `value`**; pending/failed/canceled Runs are not consumable |
| `404` | Run is outside authorized scope, absent, or has no output contract |

`Replay.structured_result` uses the same read model. It selects the latest
verification attempt and its linked, intact JSON Artifact, never the last
Conversation message. Redacted, truncated, missing or changed Artifacts fail
closed even if verification passed; retained content must match its hash.
Encode precision-sensitive large integers as strings for JavaScript consumers.

Manual Reverify can refresh checks only while the original subject is recoverable:
its question, candidate and grounding hash must match prior evidence for this Run.
Later Conversation answers are never adopted as an old report. If history/source
changes prevent that match, Reverify returns `409`; start a new Run instead.

No new database column or result table is needed: the contract is already stored
in `runs.completion_contract`, and output/evidence use existing Verification
records. Old contracts omit the new field and keep their hash. Before rolling
back, drain new contracted Runs: old code cannot enforce generation settings or
serve the result endpoint. Remove `structured_output_format` from route configuration
before starting an older parser that does not recognize it. Keep existing contracts
and evidence.

## Verification

```bash
TEST_DATABASE_URL='<dedicated-test-postgres-url>' AGENTFLOW_STRUCTURED_OUTPUT_TEST=1 \
  bash scripts/test-browser.sh structured-output.spec.ts
```

The focused browser gate uses production composition, disposable Postgres and a
strict deterministic HTTP provider. It retains desktop screenshots and
`structured-output-evidence.json`: inputs, schema, frozen contract, Run identities,
events, verification records and result responses. This proves protocol and gate
behavior, not live-provider compatibility or factual extraction quality.
