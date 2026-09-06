import assert from "node:assert/strict";
import test from "node:test";

import { createMemory, searchMemories, getMemory, mutateMemory } from "./memory-api.ts";
import { APIError } from "./api-client.ts";

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

test("memory creation sends the workspace-scoped manual record", async (t) => {
  let request = {};
  mockFetch(t, { id: "mem-1", kind: "preference", content: "Use concise release notes.", metadata: {} }, (url, options) => {
    request = {
      url: String(url),
      method: options?.method,
      headers: new Headers(options?.headers),
      body: JSON.parse(String(options?.body ?? "{}"))
    };
  });

  const created = await createMemory({
    kind: "preference",
    content: "Use concise release notes.",
    metadata: { source: "manual_workbench" }
  });

  assert.match(request.url, /\/api\/memories$/);
  assert.equal(request.method, "POST");
  assert.equal(request.headers.get("X-Workspace-ID"), "default_workspace");
  assert.deepEqual(request.body, {
    kind: "preference",
    content: "Use concise release notes.",
    metadata: { source: "manual_workbench" }
  });
  assert.equal(created.id, "mem-1");
});

test("memory search sends its recall boundary and preserves ranking evidence", async (t) => {
  let request = {};
  mockFetch(t, [{
    memory: { id: "mem-1", kind: "fact", content: "AgentFlow uses typed events.", metadata: {} },
    similarity: 0.87,
    recency_boost: 0.03,
    score: 0.9
  }], (url, options) => {
    request = { url: String(url), body: JSON.parse(String(options?.body ?? "{}")) };
  });

  const results = await searchMemories({ query: "typed event protocol", limit: 10 });

  assert.match(request.url, /\/api\/memories\/search$/);
  assert.deepEqual(request.body, { query: "typed event protocol", limit: 10 });
  assert.equal(results[0].memory.id, "mem-1");
  assert.equal(results[0].score, 0.9);
});

test("memory search normalizes a malformed collection", async (t) => {
  mockFetch(t, { results: [] });
  assert.deepEqual(await searchMemories({ query: "anything" }), []);
});

test("memory detail is scoped, uncached, and preserves tombstones and audit", async (t) => {
  const detail = { memory: { id: "mem/1", version: 3, content: "", deleted_at: "2026-09-06T00:00:00Z" }, changes: [{ action: "delete", previous_version: 2, version: 3, actor: "user", reason: "withdraw" }] };
  mockFetch(t, detail, (url, options) => {
    assert.match(String(url), /\/api\/memories\/mem%2F1$/);
    assert.equal(options.cache, "no-store");
    assert.equal(new Headers(options.headers).get("X-Workspace-ID"), "default_workspace");
  });
  assert.deepEqual(await getMemory("mem/1"), detail);
});

test("memory mutations preserve expected version and operation identity without retries", async (t) => {
  const command = { operation_id: "op", expected_version: 2, action: "delete", actor: "user", reason: "wrong" };
  let calls = 0;
  mockFetch(t, { memory: { id: "mem", version: 3 }, change: { operation_id: "op" }, applied: false }, (url, options) => {
    calls++;
    assert.match(String(url), /\/api\/memories\/mem\/mutations$/);
    assert.equal(options.method, "POST");
    assert.deepEqual(JSON.parse(options.body), command);
  });
  assert.equal((await mutateMemory("mem", command)).applied, false);
  assert.equal(calls, 1);
});

test("memory version conflicts expose their status and code for explicit refresh", async (t) => {
  const originalFetch = globalThis.fetch;
  let calls = 0;
  t.after(() => { globalThis.fetch = originalFetch; });
  globalThis.fetch = async () => {
    calls++;
    return new Response(JSON.stringify({ error: "memory changed; refresh", code: "memory_version_conflict" }), { status: 409 });
  };
  await assert.rejects(mutateMemory("mem", { operation_id: "op", expected_version: 1, action: "replace", content: "new", actor: "user", reason: "correction" }), (error) => {
    assert.ok(error instanceof APIError);
    assert.equal(error.status, 409);
    assert.equal(error.code, "memory_version_conflict");
    assert.match(error.message, /refresh/);
    return true;
  });
  assert.equal(calls, 1);
});
