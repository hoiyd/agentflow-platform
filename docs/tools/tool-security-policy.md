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
its version-checked Conversation write. Its audit event must be persisted before
the handler executes. Irreversible calls cannot use plain `allow`; they need at
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

Policy and Tool capability are frozen in Runtime Snapshot v11. Resume therefore
uses the same authority as the original Run even if the live operator file has
changed. Version 10 remains resumable for one compatibility window and applies
the current fail-closed policy because it did not capture one.

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
4. Also use `side_effect.mode=external` when execution needs the durable
   intent/effect/settlement journal; this recovery flag does not replace policy.
5. Run the shared Tool Contract and Fault Harness plus allow and deny cases.

Policy configuration never contains credential values. Filesystem sandbox,
path traversal, SSRF, and Secret resolution remain separate adapters behind the
same scope contract when those Tool types are introduced.
