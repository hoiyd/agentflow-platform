import assert from "node:assert/strict";
import test from "node:test";

import { APIError } from "./api-client.ts";
import { getRunModelRequests, getRunProjection, getRunReplay, getRunUsage, getTaskState, patchTaskState } from "./api.ts";
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

test("replay preserves durable recovery metadata", async (t) => {
  mockFetch(t, replayPayload({
    stage_checkpoints: [{ stage_id: "stage-1", status: "committed", event_cursor: 9 }],
    tool_effects: [{ tool_call_id: "call-1", status: "committed", has_result: true }]
  }));

  const replay = await getRunReplay("run-1");

  assert.equal(replay.stage_checkpoints[0].status, "committed");
  assert.equal(replay.tool_effects[0].has_result, true);
});

test("replay preserves reconciliation claims and their typed audit events", async (t) => {
  const claim = { id: "claim-1", run_id: "run-1", type: "tool.effect.reconciliation_started", sequence: 3,
    payload: { command_id: "command-1", command_hash: "hash", outcome: "pending", status: "reconciling" } };
  mockFetch(t, replayPayload({
    run_events: [claim],
    tool_effects: [{ tool_call_id: "call-1", status: "reconciling", version: 3, has_result: false }]
  }));
  const replay = await getRunReplay("run-1");
  assert.equal(replay.tool_effects[0].status, "reconciling");
  assert.equal(replay.run_events[0].type, claim.type);
  assert.equal(replay.run_events[0].payload.outcome, "pending");
});

test("replay preserves tool artifact recovery metadata", async (t) => {
  mockFetch(t, replayPayload({
    tool_artifacts: [{ id: "tool_artifact_1", run_id: "run-1", tool_call_id: "call-1", tool_name: "search", stored_byte_size: 100000 }]
  }));

  const replay = await getRunReplay("run-1");

  assert.equal(replay.tool_artifacts.length, 1);
  assert.equal(replay.tool_artifacts[0].id, "tool_artifact_1");
  assert.equal(replay.tool_artifacts[0].stored_byte_size, 100000);
});

test("replay preserves parent and child delegation topology", async (t) => {
  const relation = {
    id: "delegation-1", workspace_id: "workspace-1", conversation_id: "conversation-1",
    parent_run_id: "run-1", parent_turn_id: "turn-1", child_run_id: "run-child",
    agent_id: "agent-worker", depth: 1, status: "blocked", block_reason: "child_recovery_required", task: "work",
    summary: "done", output_ref: "run://run-child/stages/worker", timeout_ms: 120000,
    created_at: "2026-08-27T00:00:00Z", updated_at: "2026-08-27T00:00:01Z"
  };
  mockFetch(t, replayPayload({ parent_delegation: relation, child_delegations: [relation] }));

  const replay = await getRunReplay("run-1");

  assert.equal(replay.parent_delegation.child_run_id, "run-child");
  assert.equal(replay.parent_delegation.block_reason, "child_recovery_required");
  assert.equal(replay.child_delegations[0].output_ref, "run://run-child/stages/worker");
});

test("replay preserves structured task state revisions", async (t) => {
  mockFetch(t, replayPayload({
    task_state_revisions: [{ id: "revision-1", version: 1, state: { version: 1, tasks: [] } }]
  }));

  const replay = await getRunReplay("run-1");

  assert.equal(replay.task_state_revisions.length, 1);
  assert.equal(replay.task_state_revisions[0].version, 1);
});

test("replay preserves the canonical projection and invariant diagnostics", async (t) => {
  mockFetch(t, replayPayload({
    projection: {
      run: {
        run_id: "run-1", conversation_id: "conversation-1", status: "completed", verification_status: "not_required",
        active_stage_ids: [], active_turn_ids: [], active_model_call_ids: [], active_tool_call_ids: [],
        summary: replayPayload().summary, as_of_sequence: 7
      },
      usage: { ledger: { run_id: "run-1", budget: {}, totals: {}, entries: [] }, as_of_sequence: 7 },
      verification: { status: "not_required", latest_attempt: 0, evidence_count: 0, fresh_evidence_count: 0, as_of_sequence: 7 },
      as_of_sequence: 7,
      invariant_failures: [{ code: "tool_terminal_orphan", owner: "event", run_id: "run-1", sequence: 7, message: "orphan" }]
    }
  }));

  const replay = await getRunReplay("run-1");

  assert.equal(replay.projection.as_of_sequence, 7);
  assert.equal(replay.projection.invariant_failures[0].code, "tool_terminal_orphan");
});

test("projection client reads the dedicated read-model endpoint", async (t) => {
  let requestedURL = "";
  mockFetch(t, {
    run: {
      run_id: "run-1", conversation_id: "conversation-1", status: "completed", verification_status: "not_required",
      active_stage_ids: [], active_turn_ids: [], active_model_call_ids: [], active_tool_call_ids: [],
      summary: replayPayload().summary, as_of_sequence: 2
    },
    usage: { ledger: { run_id: "run-1", budget: {}, totals: {}, entries: [] }, as_of_sequence: 2 },
    verification: { status: "not_required", latest_attempt: 0, evidence_count: 0, fresh_evidence_count: 0, as_of_sequence: 2 },
    as_of_sequence: 2,
    invariant_failures: []
  }, (url) => { requestedURL = String(url); });

  const projection = await getRunProjection("run-1");

  assert.equal(projection.as_of_sequence, 2);
  assert.match(requestedURL, /\/api\/runs\/run-1\/projection$/);
});

test("task state client uses conversation-scoped get and patch endpoints", async (t) => {
  let requestedURL = "";
  mockFetch(t, {
    schema_version: 1,
    workspace_id: "default_workspace",
    conversation_id: "conversation-1",
    version: 0,
    tasks: [], decisions: [], constraints: [], blockers: [], artifact_refs: [],
    updated_at: "0001-01-01T00:00:00Z"
  }, (url) => { requestedURL = String(url); });

  const state = await getTaskState("conversation-1");

  assert.equal(state.version, 0);
  assert.match(requestedURL, /\/api\/conversations\/conversation-1\/task-state$/);
});

test("task state patch client sends the optimistic version contract", async (t) => {
  let body = "";
  mockFetch(t, {
    id: "revision-1", workspace_id: "default_workspace", conversation_id: "conversation-1",
    version: 1, previous_version: 0, patch: { expected_version: 0, operations: [] },
    state: { version: 1 }, source: { actor_type: "user" }, created_at: "2026-08-25T00:00:00Z"
  }, (_url, options) => { body = String(options.body); });

  const revision = await patchTaskState("conversation-1", {
    expected_version: 0,
    operations: [{ type: "set_goal", goal: "Keep exact state" }]
  });

  assert.equal(revision.version, 1);
  assert.deepEqual(JSON.parse(body), {
    expected_version: 0,
    operations: [{ type: "set_goal", goal: "Keep exact state" }]
  });
});

test("replay rejects a response without its required run object", async (t) => {
  mockFetch(t, replayPayload({ run: null }));

  await assert.rejects(() => getRunReplay("run-invalid"), /Invalid run replay run response/);
});

test("shared transport rejects invalid JSON at the API boundary", async (t) => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () => new Response("not-json", {
    status: 200,
    headers: { "Content-Type": "application/json" }
  });
  t.after(() => {
    globalThis.fetch = originalFetch;
  });

  await assert.rejects(() => getRunUsage("run-invalid-json"), /Failed to load run usage: invalid JSON response/);
});

test("run usage client calls the dedicated endpoint", async (t) => {
  let requestedURL = "";
  let workspaceID = "";
  mockFetch(t, {
    run_id: "run-2",
    budget: { max_tool_calls: 4 },
    totals: { tool_calls: 1 },
    entries: []
  }, (url, options) => {
    requestedURL = String(url);
    workspaceID = new Headers(options?.headers).get("X-Workspace-ID") ?? "";
  });

  const ledger = await getRunUsage("run-2");

  assert.match(requestedURL, /\/api\/runs\/run-2\/usage$/);
  assert.equal(workspaceID, "default_workspace");
  assert.equal(ledger.budget.max_tool_calls, 4);
  assert.equal(ledger.totals.tool_calls, 1);
  assert.equal(ledger.totals.open_reservations, 0);
});

test("model request client keeps capture content opt-in", async (t) => {
  const requestedURLs = [];
  mockFetch(t, { run_id: "run-3", reconstructability_status: "valid", records: [] }, (url) => {
    requestedURLs.push(String(url));
  });

  await getRunModelRequests("run-3");
  await getRunModelRequests("run-3", true);

  assert.match(requestedURLs[0], /\/api\/runs\/run-3\/model_requests$/);
  assert.match(requestedURLs[1], /\/api\/runs\/run-3\/model_requests\?include_content=true$/);
});

test("RAG search preserves fusion, reranker, relevance gate, security, context selection, citations, and no-match metadata", async (t) => {
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
    reranker: { algorithm: "heuristic", version: "heuristic-reranker-v1", config_version: "heuristic-default-v1" },
    relevance_gate: { policy: "heuristic", version: "heuristic-relevance-gate-v1", config_version: "heuristic-relevance-default-v1" },
    security: { policy_version: "rag-prompt-guard-v1", untrusted_context: true, checked_candidates: 1, blocked_candidates: 1, decisions: [{ document_id: "doc-1", chunk_id: "chunk-1", action: "blocked", reasons: ["instruction_override"] }] },
    no_match: true,
    reason: "No confident match found."
  }, (_url, options) => {
    requestBody = JSON.parse(String(options?.body ?? "{}"));
  });

  const response = await searchRAG({ query: "AUTH-7F31", knowledge_context_max_tokens: 2400 });

  assert.equal(response.fusion?.algorithm, "rrf");
  assert.equal(response.fusion?.rank_constant, 60);
  assert.equal(response.reranker?.algorithm, "heuristic");
  assert.equal(response.reranker?.config_version, "heuristic-default-v1");
  assert.equal(response.relevance_gate?.version, "heuristic-relevance-gate-v1");
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
  assert.equal(new Headers(request.options.headers).get("Content-Type"), "application/json");
  assert.equal(new Headers(request.options.headers).get("X-Workspace-ID"), "default_workspace");
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
  mockFetch(t, {
    error: "Service Unavailable",
    code: "embedding_unavailable",
    source: "model_provider",
    category: "availability",
    retryable: true,
    request_id: "req_test"
  }, () => {}, 503);

  await assert.rejects(
    () => searchRAG({ query: "failure" }),
    (error) => {
      assert.equal(error instanceof APIError, true);
      assert.match(error.message, /Failed to search knowledge: 503/);
      assert.equal(error.code, "embedding_unavailable");
      assert.equal(error.source, "model_provider");
      assert.equal(error.category, "availability");
      assert.equal(error.retryable, true);
      assert.equal(error.requestId, "req_test");
      return true;
    }
  );
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

test("RAG evaluation sends a versioned Golden Dataset", async (t) => {
  let requestBody = {};
  mockFetch(t, {
    dataset: { schema_version: "rag-golden-dataset-v1", id: "support", version: "1.0.0" },
    summary: { total: 1, answerable_cases: 1, unanswerable_cases: 0, hit_at_1: 0, hit_at_3: 1, hit_at_5: 1, misses: 0 },
    cases: [{ id: "multi-source", query: "service owner schedule", answerable: true, required_source_count: 2, best_rank: 2, hit: true, items: [] }]
  }, (_url, options) => {
    requestBody = JSON.parse(String(options?.body ?? "{}"));
  });

  const response = await runRAGEvaluation({
    dataset: {
      schema_version: "rag-golden-dataset-v1",
      id: "support",
      version: "1.0.0",
      cases: [
        {
          id: "multi-source",
          query: "service owner schedule",
          answerable: true,
          expected_sources: [{ document_id: "doc-service" }, { document_id: "doc-oncall" }],
          required_source_count: 2
        }
      ]
    },
    top_k: 3
  });

  assert.equal(requestBody.dataset.schema_version, "rag-golden-dataset-v1");
  assert.equal(requestBody.dataset.cases[0].answerable, true);
  assert.equal(requestBody.dataset.cases[0].required_source_count, 2);
  assert.equal(response.dataset.id, "support");
  assert.equal(response.cases[0].hit, true);
});
