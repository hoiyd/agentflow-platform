import assert from "node:assert/strict";
import test from "node:test";

import { createMemory, searchMemories } from "./memory-api.ts";

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
