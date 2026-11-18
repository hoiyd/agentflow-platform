import assert from "node:assert/strict";
import test from "node:test";

import { normalizeRunReplay } from "./run-replay-normalization.ts";

test("Replay keeps the evidence-backed result and normalizes absent legacy data", () => {
  const result = { run_id: "run", status: "completed", verification_status: "passed", mode: "strict", verifier_id: "report", value: { summary: "ok" } };
  assert.deepEqual(normalizeRunReplay({ ...replay, structured_result: result }).structured_result, result);
  assert.equal(normalizeRunReplay({ ...replay, structured_result: null }).structured_result, undefined);
});

const replay = {
  run: { id: "run-1", agent_id: "agent-1", conversation_id: "conversation-1", status: "completed" },
  conversation: { id: "conversation-1", title: "Replay" },
  summary: { run_id: "run-1" },
  messages: [],
  steps: [],
  run_events: []
};

test("old and incomplete Skill evidence stays safe without inventing activation", () => {
  assert.deepEqual(normalizeRunReplay(replay).projection.skill_evidence, []);
  assert.deepEqual(normalizeRunReplay({ ...replay, projection: { skill_evidence: null } }).projection.skill_evidence, []);
  const data = normalizeRunReplay({ ...replay, projection: { skill_evidence: [null, {}, {
    name: "method", hash: "hash", agent_id: "agent", bound: true,
    instructions: "unknown", activation: "unknown", resources: [null], failures: null
  }] } });
  assert.equal(data.projection.skill_evidence.length, 1);
  assert.equal(data.projection.skill_evidence[0].instructions, "not_observed");
  assert.equal(data.projection.skill_evidence[0].activation, "not_observed");
  assert.deepEqual(data.projection.skill_evidence[0].resources, []);
  assert.deepEqual(data.projection.skill_evidence[0].failures, []);
});
