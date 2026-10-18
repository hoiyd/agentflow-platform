import { expect, test } from "@playwright/test";

test("conditional knowledge metadata updates preserve the index and survive reload", async ({ page, request }) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  // Keep this corpus separate from other gates sharing the test server.
  const owned = await request.post("http://127.0.0.1:18080/api/workspaces", { data: { name: "Metadata regression" } });
  expect(owned.status()).toBe(201);
  const workspace: string = (await owned.json()).id;
  // Browser-origin HTTP uses production CORS, Workspace middleware, KnowledgeBase
  // and Postgres. No mock API responses or live provider credentials are used.
  const call = (method: string, path: string, body?: unknown, scope = workspace) => page.evaluate(async (input) => {
    const response = await fetch(`http://127.0.0.1:18080${input.path}`, {
      method: input.method,
      credentials: "include",
      headers: { "Content-Type": "application/json", "X-Workspace-ID": input.scope },
      body: input.body === undefined ? undefined : JSON.stringify(input.body)
    });
    return { status: response.status, body: await response.json() };
  }, { method, path, body, scope });
  const input = { source_key: "metadata-browser-release", title: "Metadata release guide", source_type: "markdown",
    content: "# Release\n\nDeploy AgentFlow production release safely.\n\n## Recovery\n\nRollback AgentFlow production release.",
    metadata: { product: "AgentFlow", environment: "staging" } };
  const created = await call("POST", "/api/documents", input);
  expect(created.status).toBe(201);
  const before = await call("GET", `/api/documents/${created.body.id}`);
  const path = `/api/documents/${created.body.id}/metadata`;
  const counter = await (await request.get("http://127.0.0.1:18080/__fixture/contracts")).json();
  const patch = { expected_updated_at: created.body.updated_at, metadata: { product: "AgentFlow", environment: "production" } };
  const updated = await call("PATCH", path, patch);
  expect(updated.status).toBe(200);
  expect(updated.body.updated_at).not.toBe(created.body.updated_at);
  expect(updated.body.index_identity).toEqual(created.body.index_identity);
  const retry = await call("PATCH", path, patch);
  expect(retry).toEqual(updated);
  const stale = await call("PATCH", path, { ...patch, metadata: { environment: "stale" } });
  expect(stale.status).toBe(409);
  const foreign = await call("POST", "/api/workspaces", { name: "Foreign metadata workspace" });
  expect(foreign.status).toBe(201);
  expect((await call("PATCH", path, patch, foreign.body.id)).status).toBe(404);
  const reingested = await call("POST", "/api/documents", { ...input, expected_updated_at: updated.body.updated_at,
    metadata: { product: "AgentFlow", environment: "production", release: "current" } });
  expect(reingested.status).toBe(201);
  expect(reingested.body.id).toBe(created.body.id);
  const afterCounter = await (await request.get("http://127.0.0.1:18080/__fixture/contracts")).json();
  expect(afterCounter.embedding_requests).toBe(counter.embedding_requests);
  await page.reload();
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const after = await call("GET", `/api/documents/${created.body.id}`);
  expect(after.status).toBe(200);
  expect(after.body.document.updated_at).toBe(reingested.body.updated_at);
  expect(after.body.chunks.map((chunk: { id: string }) => chunk.id)).toEqual(before.body.chunks.map((chunk: { id: string }) => chunk.id));
  for (const chunk of after.body.chunks) {
    expect(chunk.metadata.environment).toBe("production");
    expect(chunk.metadata.release).toBe("current");
  }
  const searches = [];
  for (const environment of ["staging", "production"]) {
    const result = await call("POST", "/api/rag/search", { query: "AgentFlow production release", metadata: { environment }, limit: 10 });
    expect(result.status).toBe(200);
    expect(result.body.items.length > 0).toBe(environment === "production");
    searches.push({ environment, result });
  }
  await test.info().attach("knowledge-metadata-evidence.json", {
    body: JSON.stringify({ schema: "metadata-only-browser-v1", workspace, input, patch, before, updated, retry, stale, reingested, after, searches,
      embedding_requests_before_updates: counter.embedding_requests, embedding_requests_after_updates: afterCounter.embedding_requests,
      limitations: ["controlled embedding provider and disposable Postgres", "API capability exercised from the browser, not a new metadata editor", "no expiry policy or live retrieval-quality claim"] }, null, 2),
    contentType: "application/json"
  });
});
