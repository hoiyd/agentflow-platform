import assert from "node:assert/strict";
import test from "node:test";

import { getReplayPageData } from "./replay-page-data.ts";
import { normalizeRunReplay } from "./run-replay-normalization.ts";

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

test("episode report failure does not hide the core replay", async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => String(url).endsWith("/episode")
    ? new Response(JSON.stringify({ error: "report unavailable" }), { status: 503 })
    : new Response(JSON.stringify(replay), { status: 200 });
  t.after(() => { globalThis.fetch = originalFetch; });

  const result = await getReplayPageData("run-1");

  assert.equal(result.data.run.id, "run-1");
  assert.equal(result.report, null);
  assert.match(result.reportError, /report unavailable/);
});

test("core replay failure remains fatal", async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url) => String(url).endsWith("/replay")
    ? new Response(JSON.stringify({ error: "replay unavailable" }), { status: 503 })
    : new Response(JSON.stringify({}), { status: 200 });
  t.after(() => { globalThis.fetch = originalFetch; });

  await assert.rejects(() => getReplayPageData("run-1"), /replay unavailable/);
});

test("Replay and Episode reads share the caller's cancellation signal", async (t) => {
  const originalFetch = globalThis.fetch;
  const controller = new AbortController();
  const signals = [];
  globalThis.fetch = async (url, options) => {
    signals.push(options.signal);
    return new Response(JSON.stringify(String(url).endsWith("/episode")
      ? { retrievals: {}, verification: {} } : replay), { status: 200 });
  };
  t.after(() => { globalThis.fetch = originalFetch; });
  await getReplayPageData("run-1", controller.signal);
  assert.deepEqual(signals, [controller.signal, controller.signal]);
});
