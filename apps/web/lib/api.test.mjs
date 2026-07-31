import assert from "node:assert/strict";
import test from "node:test";

import { getRunReplay, getRunUsage } from "./api.ts";
import {
  createDocument,
  deleteDocument,
  getDocument,
  listDocuments,
  runRAGEvaluation,
  searchRAG,
  uploadDocument
} from "./knowledge-api.ts";

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

function mockFetch(t, body, onRequest = () => {}, status = 200) {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async (url, options) => {
    onRequest(url, options);
    return new Response(JSON.stringify(body), {
      status,
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

test("RAG search preserves fusion, security, context selection, citations, and no-match metadata", async (t) => {
  let requestBody = {};
  mockFetch(t, {
    items: [],
    context_items: [{
      document: { id: "doc-1" },
      chunk: { id: "context_merged_1" },
      source_id: "S1",
      context_role: "matched_child",
      source_chunk_ids: ["chunk-1", "chunk-2"],
      matched_chunk_ids: ["chunk-2"],
      merged_chunk_count: 2
    }],
    citation_sources: [{
      source_id: "S1",
      document_id: "doc-1",
      document_title: "Runbook",
      chunk_id: "context_merged_1",
      source_chunk_ids: ["chunk-1", "chunk-2"]
    }],
    context_selection: {
      version: "parent-child-v1",
      max_tokens: 16000,
      tokens_used: 12,
      matched_children: 1,
      parent_chunks: 0,
      adjacent_chunks: 0,
      scope_filtered: true,
      transformation: {
        version: "context-dedup-merge-v1",
        input_chunks: 3,
        output_chunks: 1,
        duplicates_removed: 1,
        adjacent_merges: 1,
        document_groups: 1
      }
    },
    embedding: { provider: "local", model: "test", dimensions: 3, estimated: true },
    fusion: { algorithm: "rrf", version: "rrf-v1", rank_constant: 60, dense_weight: 1, lexical_weight: 1 },
    security: { policy_version: "rag-prompt-guard-v1", untrusted_context: true, checked_candidates: 1, blocked_candidates: 1, decisions: [{ document_id: "doc-1", chunk_id: "chunk-1", action: "blocked", reasons: ["instruction_override"] }] },
    no_match: true,
    reason: "No confident match found."
  }, (_url, options) => {
    requestBody = JSON.parse(String(options?.body ?? "{}"));
  });

  const response = await searchRAG({ query: "AUTH-7F31", knowledge_context_max_tokens: 2400 });

  assert.equal(response.fusion?.algorithm, "rrf");
  assert.equal(response.fusion?.rank_constant, 60);
  assert.equal(response.security?.policy_version, "rag-prompt-guard-v1");
  assert.deepEqual(response.security?.decisions?.[0].reasons, ["instruction_override"]);
  assert.equal(response.context_items?.[0].context_role, "matched_child");
  assert.equal(response.context_items?.[0].source_id, "S1");
  assert.equal(response.citation_sources?.[0].source_id, "S1");
  assert.deepEqual(response.citation_sources?.[0].source_chunk_ids, ["chunk-1", "chunk-2"]);
  assert.deepEqual(response.context_items?.[0].source_chunk_ids, ["chunk-1", "chunk-2"]);
  assert.equal(response.context_items?.[0].merged_chunk_count, 2);
  assert.equal(response.context_selection?.version, "parent-child-v1");
  assert.equal(response.context_selection?.scope_filtered, true);
  assert.equal(response.context_selection?.transformation?.adjacent_merges, 1);
  assert.equal(requestBody.knowledge_context_max_tokens, 2400);
  assert.equal(response.no_match, true);
  assert.equal(response.reason, "No confident match found.");
});

test("document list normalizes a non-array response", async (t) => {
  mockFetch(t, { documents: [] });

  assert.deepEqual(await listDocuments(), []);
});

test("document creation sends the complete JSON contract", async (t) => {
  let request = {};
  mockFetch(t, { id: "doc-1", title: "Runbook" }, (url, options) => {
    request = { url: String(url), options, body: JSON.parse(String(options?.body ?? "{}")) };
  });

  const document = await createDocument({
    title: "Runbook",
    version: "v2",
    content: "Recovery steps",
    metadata: { project: "agentflow" }
  });

  assert.match(request.url, /\/api\/documents$/);
  assert.equal(request.options.method, "POST");
  assert.equal(request.options.headers["Content-Type"], "application/json");
  assert.deepEqual(request.body, {
    title: "Runbook",
    version: "v2",
    content: "Recovery steps",
    metadata: { project: "agentflow" }
  });
  assert.equal(document.id, "doc-1");
});

test("document upload trims an optional title and sends multipart data", async (t) => {
  let requestBody;
  mockFetch(t, { id: "doc-upload", title: "Guide" }, (_url, options) => {
    requestBody = options?.body;
  });
  const file = new File(["# Guide"], "guide.md", { type: "text/markdown" });

  const document = await uploadDocument({ file, title: "  Guide  " });

  assert.ok(requestBody instanceof FormData);
  assert.equal(requestBody.get("title"), "Guide");
  assert.equal(requestBody.get("file").name, "guide.md");
  assert.equal(document.id, "doc-upload");
});

test("document detail normalizes missing chunks", async (t) => {
  mockFetch(t, { document: { id: "doc-1", title: "Runbook" }, chunks: null });

  const detail = await getDocument("doc-1");

  assert.equal(detail.document.id, "doc-1");
  assert.deepEqual(detail.chunks, []);
});

test("document deletion uses the document endpoint and DELETE method", async (t) => {
  let request = {};
  mockFetch(t, {}, (url, options) => {
    request = { url: String(url), method: options?.method };
  });

  await deleteDocument("doc-42");

  assert.match(request.url, /\/api\/documents\/doc-42$/);
  assert.equal(request.method, "DELETE");
});

test("RAG search preserves the legacy array response", async (t) => {
  const legacyItem = { document: { id: "doc-1" }, chunk: { id: "chunk-1" } };
  mockFetch(t, [legacyItem]);

  const response = await searchRAG({ query: "legacy" });

  assert.deepEqual(response.items, [legacyItem]);
  assert.equal(response.context_items, undefined);
});

test("RAG search normalizes malformed item collections", async (t) => {
  mockFetch(t, {
    items: { id: "not-an-array" },
    context_items: "not-an-array",
    context_selection: { version: "parent-child-v1", max_tokens: 100, tokens_used: 0 }
  });

  const response = await searchRAG({ query: "malformed" });

  assert.deepEqual(response.items, []);
  assert.deepEqual(response.context_items, []);
  assert.equal(response.context_selection?.max_tokens, 100);
});

test("RAG search exposes an unsuccessful HTTP status", async (t) => {
  mockFetch(t, { error: "unavailable" }, () => {}, 503);

  await assert.rejects(() => searchRAG({ query: "failure" }), /Failed to search knowledge: 503/);
});

test("RAG evaluation sends cases and preserves the result", async (t) => {
  let requestBody = {};
  mockFetch(t, {
    summary: { total: 1, hit_at_1: 1, hit_at_3: 1, hit_at_5: 1, misses: 0 },
    cases: [{ id: "auth-error", query: "AUTH-7F31", hit: true, items: [] }]
  }, (_url, options) => {
    requestBody = JSON.parse(String(options?.body ?? "{}"));
  });

  const response = await runRAGEvaluation({
    cases: [{ id: "auth-error", query: "AUTH-7F31" }],
    top_k: 3,
    min_similarity: 0.2,
    metadata: { project: "agentflow" }
  });

  assert.equal(requestBody.top_k, 3);
  assert.equal(requestBody.min_similarity, 0.2);
  assert.deepEqual(requestBody.metadata, { project: "agentflow" });
  assert.equal(response.summary.hit_at_1, 1);
  assert.equal(response.cases[0].id, "auth-error");
});
