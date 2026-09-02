# Configurable Agent Profiles

AgentFlow treats an Agent as a persisted execution profile rather than a label
attached to one prompt. A profile controls the instructions and capabilities
used by the shared Turn Engine, while orchestration mode controls how that Agent
participates in a Run.

## Profile Fields

| Field | Runtime effect |
| --- | --- |
| `name` | Human-readable identity shown in the workbench, Replay, and Episode Report. |
| `description` | Declares the Agent's responsibility and supplies routing evidence in Multi mode. |
| `system_prompt` | Defines the Agent persona and task-specific instructions used by its model calls. |
| `tools` | Per-Agent allowlist drawn from installed platform tools. Unknown tools are rejected when the profile is saved. |
| `memory_enabled` | Enables scoped semantic Memory retrieval before a Turn. |
| `retrieval_enabled` | Enables Knowledge/RAG retrieval before a Turn. |

The workbench can create, edit, select, and archive custom profiles. The four
built-in profiles remain available as stable defaults and cannot be archived.

## Behavior by Execution Mode

- **Single:** the selected Agent executes one direct Turn with its prompt,
  tools, and retrieval policies.
- **Multi:** all active profiles are frozen as Router candidates. After plan
  approval, the Router selects a Worker from those candidates using the task,
  approved plan, Agent description, and routing policy. An explicitly requested
  Agent remains the initial profile for the Run.
- **Loop:** the selected Agent supplies the persona and capabilities for bounded
  Act stages while Observe, Plan, Review, and Decide retain their stage-specific
  prompts.

Mode changes orchestration shape; it does not create a separate implementation
of Agent configuration, Retrieval, Tool execution, Context Assembly, Budget, or
Tracing.

## Frozen Run Semantics

Creating a Run captures the effective Agent profile in its Runtime Snapshot.
Multi mode also captures every candidate profile. The Snapshot includes Agent
identity, description, system prompt, tool names, and Memory/RAG switches,
together with the native execution protocol, model identity, tool schemas,
context policy, and Run Budget.

Editing or archiving a profile later does not rewrite an existing Run. Resume
restores the frozen Agent configuration and verifies that every required tool is
still installed with the same captured schema. Credentials and live tool
handlers are deployment policy and are not persisted in the Snapshot.

## Agent API

Create a reusable profile:

```http
POST /api/agents
Content-Type: application/json
```

```json
{
  "name": "Incident Responder",
  "description": "Diagnoses production incidents from runbooks and runtime evidence.",
  "system_prompt": "Act as a concise incident responder. Separate evidence from assumptions and return ordered recovery steps.",
  "tools": ["calculator", "get_current_time"],
  "memory_enabled": true,
  "retrieval_enabled": true
}
```

Update only the fields that should change:

```http
PATCH /api/agents/{id}
Content-Type: application/json
```

```json
{
  "system_prompt": "Prioritize evidence from the incident runbook and identify missing operational data.",
  "tools": ["get_current_time"]
}
```

Archive a custom profile:

```http
DELETE /api/agents/{id}
```

Archive is intentionally different from deleting historical execution data.
Replay and Episode Reports continue to resolve the Agent configuration captured
for previous Runs.

## Two Tool-Control Layers

AgentFlow separates platform availability from Agent permission:

1. The Tool Manager enables or disables installed tools globally and persists
   that operator configuration.
2. Each Agent profile selects an allowlist from the currently installed tools.

The Tool Catalog compiles one normalized JSON Schema contract for each Binding.
The Tool Executor validates and canonicalizes model arguments against that
contract before budget accounting or handler execution, then owns per-call
timeout, result-size limits, panic recovery, typed errors, Run Events, and
serial/read-only/keyed concurrency. Tool name, description, normalized schema,
schema version, and definition revision are frozen per Run; the handler and
deployment limits stay live.

## Current Boundaries

- Agent profiles are not an authorization boundary. The HTTP API still requires
  a trusted deployment boundary.
- Agent profiles and the installed Tool catalog are currently global platform
  resources. `X-Workspace-ID` scopes Runs and data access, but it does not yet
  create a per-Workspace Agent registry.
- Profiles select from installed in-process tools; remote tool discovery and
  tenant-specific tool registries are not implemented.
- The Router can use deterministic profile matching or an LLM-backed policy,
  but routing quality is not yet calibrated against a versioned routing dataset.

See [Execution modes](execution-modes.md) for orchestration behavior,
[Tool Execution Policy](execution-controls.md#7-tool-execution-policy), and
[Internal terms](../architecture/terms.md#runtime-snapshot) for Snapshot ownership.
