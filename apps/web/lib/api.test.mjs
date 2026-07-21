import assert from "node:assert/strict";
import test from "node:test";

import { getRunReplay, getRunUsage } from "./api.ts";

function replayPayload(overrides = {}) {
  return {
    run: { id: "run-1", agent_id: "agent-1", conversation_id: "conversation-1", status: "completed" },
    conversation: { id: "conversation-1", title: "Budget test" },
    messages: [],
    steps: [],
    summary: { run_id: "run-1", total_duration_ms: 10, total_tokens: 0, prompt_tokens: 0, completion_tokens: 0, token_usage_estimated: false, llm_calls: 0, tool_calls: 0, error_count: 0 },
    run_events: [],
    verification_evidence: [],
    verification_artifacts: [],
    ...overrides
  };
}

function mockFetch(t, body, onRequest = () => {}) {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, options) => {
    onRequest(url, options);
    return new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" }
    });
  };
  t.after(() => {
    globalThis.fetch = originalFetch;
  });
}

test("legacy replay receives an empty usage ledger", async (t) => {
  mockFetch(t, replayPayload());

  const replay = await getRunReplay("run-1");

  assert.deepEqual(replay.usage_ledger, {
    run_id: "run-1",
    budget: {},
    totals: {
      model_calls: 0,
      tool_calls: 0,
      prompt_tokens: 0,
      completion_tokens: 0,
      total_tokens: 0,
      estimated_cost_micros: 0,
      open_reservations: 0
    },
    entries: []
  });
});

test("replay preserves frozen budget and settled usage", async (t) => {
  mockFetch(t, replayPayload({
    usage_ledger: {
      run_id: "run-1",
      budget: { max_model_calls: 8, max_total_tokens: 12000 },
      totals: { model_calls: 2, total_tokens: 3200, open_reservations: 0 },
      entries: [{ id: "usage-1", run_id: "run-1", operation_id: "model-1", kind: "model.settlement", purpose: "primary", timestamp: "2026-07-20T00:00:00Z" }],
      updated_at: "2026-07-20T00:00:00Z"
    }
  }));

  const replay = await getRunReplay("run-1");

  assert.equal(replay.usage_ledger.budget.max_model_calls, 8);
  assert.equal(replay.usage_ledger.totals.model_calls, 2);
  assert.equal(replay.usage_ledger.totals.total_tokens, 3200);
  assert.equal(replay.usage_ledger.totals.prompt_tokens, 0);
  assert.equal(replay.usage_ledger.entries.length, 1);
});

test("run usage client calls the dedicated endpoint", async (t) => {
  let requestedURL = "";
  mockFetch(t, {
    run_id: "run-2",
    budget: { max_tool_calls: 4 },
    totals: { tool_calls: 1 },
    entries: []
  }, (url) => {
    requestedURL = url instanceof Request ? url.url : String(url);
  });

  const ledger = await getRunUsage("run-2");

  assert.match(requestedURL, /\/api\/runs\/run-2\/usage$/);
  assert.equal(ledger.budget.max_tool_calls, 4);
  assert.equal(ledger.totals.tool_calls, 1);
  assert.equal(ledger.totals.open_reservations, 0);
});
