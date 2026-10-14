# Skill Invocation and Dependency Readiness

Skill discovery, invocation intent, dependency readiness and execution authorization
are separate boundaries. A method never enables a Tool or bypasses its Policy.

## Invocation Contract

| Effective policy | Model selection | Explicit user invocation |
| --- | --- | --- |
| `automatic` (default) | Bound metadata is advertised; `skill_load` may activate it | `/skill:<name>` at the start of the task |
| `explicit_only` | Metadata is withheld; a guessed `skill_load` receives `skill_invocation_denied` | Same prefix, only for a bound method |

The Workspace owner sets a per-bound-method policy in **Configure > Skills**.
The package may require a stricter policy; neither the owner nor model can relax
it. After explicit activation, `skill_load` may confirm the already active method
and `skill_read` may read its frozen resources. Invocation applies to this Run's
Agent, not unrelated Agents or future Runs.

The Single-mode Skill picker lists bound methods with their policy and dependency
failures. Unready methods cannot be selected; saved bindings remain visible.
**Automatic** (or **No explicit skill** when all bindings require explicit input)
removes the prefix only; it does not change configuration. Multi/Loop users can
type the same prefix; no Agent configuration controls are added to those modes.

Only the original user task supplies invocation intent. Generated plans, Worker
prompts, answer text, Tool results and Skill descriptions cannot impersonate that
prefix. Current Stage input and original Skill input are separate in the Turn
request; clarification checkpoints preserve the latter. User task text is not
expanded into macros or executed as commands.
Steering messages and clarification answers do not select a new explicit-only
method mid-Run; a new follow-up task can carry its own prefix.

This controls loading a method, not whether the model independently suggests a
similar action. Every real action still passes through Tool authorization.

## Package and API Fields

Supported package restriction in `SKILL.md`:

```yaml
disable-model-invocation: true
```

Supported Codex-style restriction in optional `agents/openai.yaml`:

```yaml
policy:
  allow_implicit_invocation: false
```

Both require literal YAML `true`/`false`. Malformed/duplicate/unsupported policy
keys, links and oversized metadata fail closed, not back to automatic loading.
The optional file is bounded to 4 KiB and never injected into model Context.
Its normalized restriction participates in package identity. Descriptive interface
metadata does not grant capabilities; foreign `dependencies` are rejected rather
than silently treated as ready. Declare actual Binding names with existing
`metadata.agentflow-required-tools`. `allowed-tools`, model-only invocation,
forks/hooks and Tool pre-approval remain unsupported; see [Skill Checker](skill-checker.md).

Agent POST/PATCH accepts:

```json
{"skills":["<method-name>"],"skill_policies":{"<method-name>":"explicit_only"}}
```

Only `automatic` and `explicit_only` values for bound names are valid. Omitting
`skill_policies` preserves settings; `{}` restores binding defaults, not package
restrictions. Removing a binding removes its policy. Configure sends the complete
map when editing it.

## Readiness and Frozen Runs

`GET /api/skills` returns `invocation_policy` and `tool_dependencies`: Tool name,
current Workspace `ready`, exclusion `reason`, and `agent_binding_required`.
Reasons distinguish not installed, operator/Workspace disabled, missing credentials
and Policy deny. The UI additionally checks the draft/current Agent allowlist;
runtime-owned harness Tools do not require a fabricated profile binding.
Readiness is a current view, not a guarantee of argument-specific Scope/approval or
future availability. Fetch/validation failures remain visible, never clear bindings.

Saving/starting a Run retains existing dependency validation. Actual activation
checks the Turn's effective ready capabilities where supplied; no missing Tool is
installed or enabled. Executor authorization still checks current grants per call.

Run Snapshots retain package restrictions and each Agent's policy map. Resume
uses those frozen settings and prior Agent-owned explicit Manifest evidence,
not mutable package files or the current profile's invocation defaults. Current
Workspace/Tool revocation remains an independent execution boundary. Old snapshots
without policy fields retain automatic semantics and unchanged package hashes.

The additive `agents.skill_policies JSONB` column is created/migrated idempotently
on API startup. Before rolling back, stop or drain policy-aware Runs: older code
cannot enforce the new invocation restrictions. Retain this column and historical
Snapshots; no rewriting of existing bindings/content is required.

## Evidence

The focused browser gate uses production Next.js/Go composition, real Bindings,
disposable Postgres and a strict deterministic provider. It checks explicit vs
model selection, generated-prefix rejection, dependency errors, all three modes,
policy save/reload and old Snapshot stability. It retains a desktop screenshot
and `skill-policy-evidence.json` with identities, inputs, frozen config and events.

```bash
TEST_DATABASE_URL='<dedicated-test-postgres-url>' AGENTFLOW_SKILL_POLICY_TEST=1 \
  bash scripts/test-browser.sh skill-policy.spec.ts
```

Backend checks additionally cover restrictive metadata failures, resource identity,
Agent isolation and Resume after package/profile changes. This is protocol and
inclusion evidence, not live-model task-quality calibration or a safety classifier.
