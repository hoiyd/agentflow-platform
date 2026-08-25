# Structured Durable Task State

Structured Task State keeps execution-critical facts outside conversation
summaries. It belongs to a Conversation and can therefore survive multiple
Runs, compaction, process restart, and Resume without being inferred again from
natural language.

## Data Model

The current `TaskState` contains:

- one current goal;
- tasks with stable IDs, status, details, and Artifact references;
- append-only decisions, optionally superseding an earlier decision;
- constraints with stable IDs;
- open or resolved blockers;
- Conversation-level Artifact references.

`TaskState` is not an independently mutable row. Every accepted update creates
an immutable `TaskStateRevision` containing the typed patch, resulting state
snapshot, actor/source metadata, previous version, and new version. Keeping the
resulting snapshot makes arbitrary historical reconstruction a direct lookup;
the system does not replay model prose to rebuild a version.

## Typed Patch Protocol

Callers submit `expected_version` and an ordered operation list. The supported
operations are:

| Group | Operations |
| --- | --- |
| Goal | `set_goal`, `clear_goal` |
| Tasks | `upsert_task`, `set_task_status`, `remove_task` |
| Decisions | `add_decision` |
| Constraints | `upsert_constraint`, `remove_constraint` |
| Blockers | `upsert_blocker`, `resolve_blocker`, `remove_blocker` |
| Artifacts | `add_artifact_ref`, `remove_artifact_ref` |

There is deliberately no `replace_state` operation. IDs, schema version,
Conversation/Workspace ownership, revision number, source metadata, and update
time are runtime-owned. The state has field limits, at most 50 operations per
patch, and a 16 KB serialized hard limit so it remains suitable for model
context.

The Store compares `expected_version` while holding the File lock or a
Postgres `FOR UPDATE` lock on the owning Conversation. A stale writer receives
`task_state_version_conflict`; it must reload and intentionally rebase rather
than silently overwriting another actor's change.

## Runtime Integration

New Runs enable Structured Task State context through Runtime Snapshot v8.
Runs created with v1-v7 snapshots keep Task State context and its Tool disabled
when resumed, preserving their original protocol. Native executors also freeze
the runtime-owned `update_task_state` Tool; the text-only LangChainGo executor
can read Task State but does not advertise a Tool it cannot execute. The Tool
is not user-toggleable: it is a harness capability backed by the same versioned
Store contract as the HTTP API. Tool calls derive Conversation, Run, Stage,
Turn, and Agent provenance from trusted runtime scope instead of accepting
those identities from model arguments.

Before every physical Model Call, Context Assembly reloads the latest state.
This matters when a Tool updates state between the initial Tool-selection call
and the Tool-result response. A non-empty state is injected as bounded
structured JSON inside `<task_state>` and is required context. The current user
request wins if it conflicts with stale Task State.

The Context Manifest stores only a source reference such as
`conversation_id:v3`, token metadata, and the `structured_json`
transformation. It does not duplicate Goal, Task, Decision, Constraint, or
Blocker text. Model Request Capture remains the policy-controlled source for
the exact transmitted content.

## Persistence and Replay

File Store persists `task_state_revisions` in the existing atomic JSON save.
Postgres stores one immutable row per `(conversation_id, version)` with JSONB
patch, state, and source records. Conversation deletion removes the full
revision chain in both adapters.

Run Replay includes the Conversation's ordered `task_state_revisions` timeline.
Each revision records its source Run when a model applied it. Because Task State
is Conversation-scoped, the Replay timeline can also contain later revisions;
the Context Manifest reference identifies the exact version seen by each Model
Call.

Successful model-originated updates emit `task_state.updated` after the
Revision is durable. The Revision Store remains authoritative if event
publication fails; retrying the original patch then returns a version conflict
instead of applying it twice.

The Tool declares a durable side effect, so Stage recovery uses the existing
Tool Effect Journal. Replaying the same Tool Call returns its committed result
without appending another Revision; a different stale call still receives a
version conflict.

## HTTP API

```text
GET   /api/conversations/{id}/task-state
PATCH /api/conversations/{id}/task-state
GET   /api/conversations/{id}/task-state/revisions
GET   /api/conversations/{id}/task-state/revisions/{version}
```

`GET task-state` returns an empty version-0 state before the first patch.
`PATCH` returns the committed Revision. All endpoints use the mandatory
Workspace-scoped Store view; a Conversation outside the resolved Workspace is
reported as not found.

## Frontend Inspection

The Conversation top bar opens a read-only Task State Inspector with the
current Goal, Task status, Decisions, Constraints, Blockers, Artifact
references, and Revision version. Conversation loads and completed execution
paths refresh the projection; users can also refresh it explicitly.

Run Replay shows version transitions and operation types. It labels Revisions
created by the selected Run separately from the wider Conversation history so
later or user-originated changes are not attributed to that Run. Rendering is
bounded while the API remains the complete historical source.

The first UI remains inspect-only. Manual correction continues through the
typed PATCH API; a richer conflict-aware editor is intentionally deferred.

Example patch:

```json
{
  "expected_version": 2,
  "operations": [
    {
      "type": "set_task_status",
      "task_id": "backend-tests",
      "task_status": "completed"
    },
    {
      "type": "add_decision",
      "decision": {
        "id": "storage-boundary",
        "statement": "Keep Task State Conversation-scoped"
      }
    }
  ]
}
```

## Failure Boundaries

- Invalid operations fail before a Revision is appended.
- Stale expected versions return HTTP `409` or a Tool error with the current
  version; no implicit merge is attempted.
- Invalid patches and source provenance return classified validation errors;
  unknown persistence failures remain internal errors rather than `400`.
- A source Run or Message from another Conversation is rejected.
- Task State load failure fails Context Assembly instead of silently dropping
  potentially critical constraints.
- Task State does not replace Messages, Runtime Snapshot, Run Events, Memory,
  Context Manifest, or Verification Evidence. Each remains authoritative for
  its own contract.
