import assert from "node:assert/strict";
import test from "node:test";
import { readChatEventStream } from "./run-stream.ts";
import { applyToolProgress, restoreToolProgress } from "./tool-progress.ts";
import { normalizeRunProjection } from "./run-replay-normalization.ts";

// Failure inventory: duplicate/old events cannot regress status; no-progress
// tools stay invisible; wrong Runs stay isolated; reload preserves latest facts.
test("durable Tool progress and terminal events keep final answers separate", async () => {
  const events = [];
  for (const [sequence, type, payload] of [
    [1, "tool.progress", { tool_name: "reader", tool_call_id: "call", phase: "reading", completed: 2, total: 3 }],
    [2, "tool.completed", { tool_name: "reader", tool_call_id: "call", result: "canonical" }]
  ]) {
    await readChatEventStream(new Response(`data: ${JSON.stringify({ schema_version: 1, type, run_id: "r", turn_id: "t", sequence, payload })}\n\n`), item => events.push(item));
  }
  assert.equal(events[0].type, "tool_progress");
  let items = applyToolProgress([], events[0], "r");
  assert.equal(items[0].phase, "reading");
  items = applyToolProgress(items, events[1], "r");
  assert.equal(items[0].status, "completed");
  assert.equal(items[0].completed, 2);
  assert.deepEqual(applyToolProgress(items, events[0], "r"), items);
  assert.deepEqual(applyToolProgress([], events[1], "r"), []);
  assert.deepEqual(applyToolProgress(items, { ...events[0], run_id: "other" }, "r"), items);
  assert.deepEqual(restoreToolProgress(items, [{ ...items[0], sequence: 1, status: "running" }], "r"), items);
  assert.deepEqual(restoreToolProgress(items, [], "other"), []);
  const snapshot = normalizeRunProjection({ tool_progress: items }, { id: "r", status: "completed" }, { run_id: "r" }, {});
  assert.deepEqual(snapshot.tool_progress, items);
});
