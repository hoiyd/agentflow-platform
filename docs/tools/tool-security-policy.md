# Tool Security Policy and Scope

AgentFlow authorizes every Tool Call in the shared Executor before Run Budget
accounting, side-effect intent creation, or handler execution. Effective
authority is the intersection of three independent controls:

1. Platform enablement decides which installed Tools are available.
2. The Agent allowlist decides which available Tools the model may select.
3. Tool Security Policy decides whether a selected call may execute and what
   resources, network targets, and credential scopes it may use.

An Agent allowlist can remove authority but cannot grant it. User messages,
retrieved knowledge, Memory, web content, Tool results, and remote Tool metadata
are untrusted data and cannot mutate these controls.

The Executor additionally denies data-transmission Tools after private Run data
has been exposed, and rejects common credential-shaped outbound arguments before
execution. These restrictions cannot be overridden by a claimed approval in an
Agent prompt. See [Agent instruction/data boundaries](../operations/agent-security.md)
for the conservative Run-scoped behavior, restoration and limitations.

## Capability Contract

Every local Binding owns a trusted `security` declaration. Its capability
budget has four dimensions:

| Dimension | Meaning | Enforcement owner |
| --- | --- | --- |
| Scope | Resource access, network targets, and logical credential scopes | Tool Security Policy |
| Rate | Risk classification (`run_budgeted` or `elevated`) | Tool Security Policy; numeric limits remain in Run Budget |
| Reversibility | `reversible`, `compensatable`, or `irreversible` | Tool Security Policy and side-effect recovery |
| Visibility | `run`, `user`, or `operator` evidence visibility | Tool Security Policy and tracing |

The declaration also includes source (`local` or `remote`), side-effect class,
approval mode, and audit level. `Descriptor.Security.Scope` is the maximum
authority. A Binding may use `ResolveScope` to derive a narrower target for one
call; the Executor rejects resolver errors, panics, and scope expansion before
the handler runs.

Credential scopes are logical names only. Secret values never enter the
Descriptor, Runtime Snapshot, Execution Request JSON, policy decision, or Run
Event. A Tool that declares a credential scope must receive the same logical
grant from a trusted resolver; a missing grant fails closed. There is no
ambient-environment fallback.

## Tavily Credential and Egress Boundary

`TAVILY_API_KEY` is read from the process environment through `credential.Value`.
When unset, the runtime does not grant the logical `tavily_search` scope. The
value is never placed in Tool configuration, a Descriptor, a Snapshot, or a
Tool execution request. The trusted `TavilyClient` accepts the credential only
at construction and sends it in the Authorization header to the fixed
`https://api.tavily.com/search` endpoint. It uses the shared governed egress
transport: all DNS answers must be public, the connection uses a validated IP
while TLS verifies the original hostname, and environment proxies are disabled.
It rejects all redirects, bounds requests and responses, and returns only safe
typed errors and redacted JSON. The endpoint and Bearer-header format follow
the [Tavily Search API](https://docs.tavily.com/documentation/api-reference/endpoint/search).

These are application-level [execution boundaries](../operations/execution-boundaries.md),
not an OS sandbox or protection for arbitrary third-party network code.

The built-in `web_search` Binding uses this client through the existing Catalog
and Executor. It is enabled by default and must also appear in an Agent's Tool
allowlist. Newly seeded `Field Researcher` Agents include it; existing Agents
retain their saved allowlists and may need it enabled in Agent configuration.
New default Tool configs include a rule limited to the exact Tavily network
target and `tavily_search` credential scope. Existing custom policies
must add the same rule explicitly; a missing grant is denied before egress.
An enabled Binding with no client reports `credential_unavailable`; it remains
listed for operators but is excluded from model definitions and new Run
snapshots. Existing Runs with a frozen search Tool fail Resume explicitly if
the credential is no longer available.

The Binding accepts a bounded query, 1-5 results (default 3), and optional
`include_domains` or `time_range` filters. It fixes Tavily search depth to
`basic` and excludes provider-generated answers, raw content, and images.
Results contain a source ID, title, HTTPS URL, bounded snippet, provider rank,
and an `untrusted_external_content` marker. Complete successful results receive
stable Run-scoped `[W#]` aliases; final-answer citations resolve only against
the actual Tool event and sources selected in the final model Context.
This proves provenance, not factual correctness. See
[Web source citations](web-source-citations.md). No results, provider 429, timeout, 5xx, and
malformed responses produce typed failures without returning provider error bodies.

An opt-in live check exercises Manager -> Catalog -> Executor -> Tavily using
`TAVILY_LIVE_TEST=1 go test ./internal/tool -run '^TestWebSearchLiveTavily$' -count=1 -v`
from `apps/api` after loading `TAVILY_API_KEY` into the process environment.
It is skipped by default and consumes one Tavily search credit when enabled.

## Default Policy

The default policy permits bounded, local, side-effect-free computation and
read access to declared Run, Conversation, or Workspace resources. It denies
the following unless an exact operator-owned Tool rule grants them:

- local writes and destructive actions;
- filesystem and external-service resources;
- internal or external network targets;
- credential scopes and elevated-rate capabilities;
- every `remote` Tool, even when the Agent selected it.

The built-in `update_task_state` Tool has an explicit `allow_and_log` rule for
its version-checked Conversation write. New default Tool configs also include
a narrow `allow` rule for `web_search`, which still requires a Tavily credential
and an Agent allowlist entry. The task-state audit event must be persisted
before the handler executes. Irreversible calls cannot use plain `allow`; they need at
least explicit `allow_and_log` authorization. `ask` and `human_only` are
reserved in the first version and return a typed `approval_required` result
until a durable approval flow exists.

## Operator Configuration

`TOOL_CONFIG_PATH` points to the JSON file that owns enablement and policy.
Omitting `security_policy` preserves the built-in fail-closed defaults. A
configured policy uses one exact rule per Tool:

```json
{
  "enabled_tools": ["calculator", "get_current_time"],
  "security_policy": {
    "version": "operator-tools-v1",
    "default_action": "allow",
    "rules": [
      {
        "id": "builtin-task-state-write",
        "tool": "update_task_state",
        "action": "allow_and_log",
        "capability": {
          "source": "local",
          "scope": {
            "resources": [
              {"kind": "conversation", "name": "task_state", "access": "write"}
            ],
            "network": {"mode": "none"}
          },
          "side_effect_class": "internal_write",
          "rate": "run_budgeted",
          "reversibility": "compensatable",
          "visibility": "user",
          "approval_mode": "none",
          "audit_level": "full"
        }
      }
    ]
  }
}
```

Policy and Tool capability have been frozen since Runtime Snapshot v11. Resume
therefore uses the same authority as the original Run even if the live operator
file has changed. Only the current Runtime Snapshot schema is resumable; older
snapshots remain readable through Replay.

## Decisions and Replay

The evaluator returns `allow`, `allow_and_log`, `ask`, `deny`, or `human_only`.
Every production call records `tool.policy_evaluated` between `tool.started`
and its terminal Tool event. Replay exposes policy version, rule ID, action,
fixed reason, classifications, and scope counts. It deliberately omits resource
names, network targets, credential scope names, arguments, and Secret values.

Stable typed failures distinguish policy denial, invalid scope, unavailable
credential scope, required approval, and audit persistence failure. Denied
calls do not consume Tool Budget and cannot create an external side effect.

## Adding a Tool

1. Declare maximum local authority in `Descriptor.Security`.
2. Add `ResolveScope` when arguments select a resource, target, or credential.
3. Add an operator rule for remote, write, network, filesystem, credential,
   elevated-rate, or irreversible capability.
4. Declare `Security.SideEffect=internal_write` for local runtime state writes,
   or `external_write`/`destructive` for external effects. `JournalMode()` derives
   the recovery boundary; Bindings do not separately set a mode. Writes require
   a durable journal, Run and Tool Call identity. Internal
   writes accept a real Stage or Turn owner; external writes require a Stage.
   These recovery declarations do not replace security policy. Retry and
   compensation callbacks remain external-only.
5. Run the shared Tool Contract and Fault Harness plus allow and deny cases.

Catalog validates the authoritative classification before any model call.
Recovery flags and callbacks must agree and remain external-only. Journal mode
is derived, so a Binding cannot omit or contradict it:

| Security side-effect class | Derived journal mode |
| --- | --- |
| `none` (including the default capability) | omitted; no journal |
| `internal_write` | `internal` |
| `external_write` or `destructive` | `external` |

In particular, `internal_write` always derives `internal`; there is no second
authoring field that can incorrectly demand a nonexistent Single Stage.
Persisted Snapshot modes and definition-digest JSON retain their existing
format. Classification changes still change the digest and fail frozen checks;
this refactor alone does not change existing production Tool revisions.

Policy configuration never contains credential values. Filesystem sandbox,
path traversal, SSRF, and Secret resolution remain separate adapters behind the
same scope contract when those Tool types are introduced.
