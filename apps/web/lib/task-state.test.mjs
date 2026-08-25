import assert from "node:assert/strict";
import test from "node:test";

import {
  hasTaskStateFacts,
  partitionTaskStateRevisions,
  taskStateCounts,
  taskStateOperationLabel,
  visibleTaskStateRevisions
} from "./task-state.ts";

function state(overrides = {}) {
  return {
    schema_version: 1,
    workspace_id: "workspace",
    conversation_id: "conversation",
    version: 2,
    tasks: [],
    decisions: [],
    constraints: [],
    blockers: [],
    artifact_refs: [],
    updated_at: "2026-08-25T00:00:00Z",
    ...overrides
  };
}

test("task state facts and counts distinguish active work", () => {
  assert.equal(hasTaskStateFacts(state()), false);
  const active = state({
    goal: "Ship the inspector",
    tasks: [
      { id: "one", title: "Build", status: "in_progress" },
      { id: "two", title: "Verify", status: "completed" }
    ],
    blockers: [{ id: "blocked", description: "Waiting", status: "open" }]
  });
  assert.equal(hasTaskStateFacts(active), true);
  assert.deepEqual(taskStateCounts(active), { openTasks: 1, completedTasks: 1, openBlockers: 1 });
});

test("task state revisions separate current run changes from conversation history", () => {
  const revisions = [
    { version: 1, source: { actor_type: "model", run_id: "run-1" } },
    { version: 2, source: { actor_type: "user" } },
    { version: 3, source: { actor_type: "model", run_id: "run-2" } }
  ];
  const grouped = partitionTaskStateRevisions(revisions, "run-1");
  assert.deepEqual(grouped.fromRun.map((revision) => revision.version), [1]);
  assert.deepEqual(grouped.conversationHistory.map((revision) => revision.version), [2, 3]);
  assert.equal(taskStateOperationLabel("set_task_status"), "set task status");
});

test("task state replay keeps current run evidence while bounding conversation history", () => {
  const revisions = Array.from({ length: 40 }, (_, index) => ({
    id: `revision-${index + 1}`,
    version: index + 1,
    source: { actor_type: "model", run_id: index < 25 ? "run-1" : "run-2" }
  }));
  const visible = visibleTaskStateRevisions(revisions, "run-1");
  assert.equal(visible.length, 30);
  assert.equal(visible[0].version, 6);
  assert.equal(visible.at(-1).version, 40);
});
