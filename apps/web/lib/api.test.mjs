import assert from "node:assert/strict";
import test from "node:test";

import { getRunReplay, getRunUsage, searchRAG } from "./api.ts";

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
    requestedURL = String(url);
  });

  const ledger = await getRunUsage("run-2");

  assert.match(requestedURL, /\/api\/runs\/run-2\/usage$/);
  assert.equal(ledger.budget.max_tool_calls, 4);
  assert.equal(ledger.totals.tool_calls, 1);
  assert.equal(ledger.totals.open_reservations, 0);
});

test("RAG search preserves fusion, security, and no-match metadata", async (t) => {
  mockFetch(t, {
    items: [],
    embedding: { provider: "local", model: "test", dimensions: 3, estimated: true },
    fusion: { algorithm: "rrf", version: "rrf-v1", rank_constant: 60, dense_weight: 1, lexical_weight: 1 },
    security: { policy_version: "rag-prompt-guard-v1", untrusted_context: true, checked_candidates: 1, blocked_candidates: 1, decisions: [{ document_id: "doc-1", chunk_id: "chunk-1", action: "blocked", reasons: ["instruction_override"] }] },
    no_match: true,
    reason: "No confident match found."
  });

  const response = await searchRAG({ query: "AUTH-7F31" });

  assert.equal(response.fusion?.algorithm, "rrf");
  assert.equal(response.fusion?.rank_constant, 60);
  assert.equal(response.security?.policy_version, "rag-prompt-guard-v1");
  assert.deepEqual(response.security?.decisions?.[0].reasons, ["instruction_override"]);
  assert.equal(response.no_match, true);
  assert.equal(response.reason, "No confident match found.");
});
