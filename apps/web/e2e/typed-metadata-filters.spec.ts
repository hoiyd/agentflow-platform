import { expect, test } from "@playwright/test";
import type { components } from "../lib/api-contract.gen";
import { defaultWorkspaceID } from "./fixtures/workspace-agent";

test("typed metadata eligibility survives browser requests, rejected input and reload", async ({ page, request }, testInfo) => {
  const version = "knowledge-metadata-v1";
  const productScope = `AgentFlow typed filter ${testInfo.repeatEachIndex}`;
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const call = (method: string, path: string, body?: unknown) => page.evaluate(async input => {
    const response = await fetch(`http://127.0.0.1:18080${input.path}`, {
      method: input.method, credentials: "include", headers: { "Content-Type": "application/json", "X-Workspace-ID": input.workspace },
      body: bodyString(input.body)
    });
    return { status: response.status, body: await response.json() };
    function bodyString(value: unknown) { return typeof value === "string" ? value : value === undefined ? undefined : JSON.stringify(value); }
  }, { method, path, body, workspace });
  const corpus: Array<{ input: components["schemas"]["DocumentIngestRequest"]; created: Awaited<ReturnType<typeof call>> }> = [];
  for (const product of [productScope, "Other"]) {
    const input: components["schemas"]["DocumentIngestRequest"] = {
      title: `Typed recovery ${product} ${testInfo.repeatEachIndex}`, source_key: `typed-recovery-${product}-${testInfo.repeatEachIndex}`,
      content: "ROLLBACK-742 production rollback procedure: restore the saved release.", metadata_schema_version: version,
      metadata: { product, environment: "production", priority: 9, tags: ["release"], published_at: "2026-10-18T04:00:00Z" }
    };
    const created = await call("POST", "/api/documents", input);
    expect(created.status).toBe(201);
    corpus.push({ input, created });
  }
  const input: components["schemas"]["KnowledgeSearchRequest"] = {
    query: "ROLLBACK-742", limit: 5, metadata: { environment: "production" },
    metadata_filter: { schema_version: version, where: { all: [
      { field: "document.metadata.product", op: "eq", value: productScope },
      { field: "chunk.metadata.tags", op: "contains", value: "release" },
      { field: "document.metadata.published_at", op: "gte", value: "2026-10-18T12:00:00+08:00" }
    ] } }
  };
  const before = await call("POST", "/api/rag/search", input);
  expect(before.status).toBe(200);
  expect(before.body.items.map((item: { document: { id: string } }) => item.document.id)).toEqual([corpus[0].created.body.id]);
  expect(before.body.context_items.every((item: { document: { id: string } }) => item.document.id === corpus[0].created.body.id)).toBe(true);
  const counterBefore = await (await request.get("http://127.0.0.1:18080/__fixture/contracts")).json();
  const invalid = await call("POST", "/api/rag/search", { ...input, metadata_filter: { schema_version: version, where: { field: "document.metadata.workspace_id", op: "eq", value: "foreign" } } });
  expect(invalid.status).toBe(400);
  expect(invalid.body.code).toBe("invalid_metadata_filter");
  const counterAfter = await (await request.get("http://127.0.0.1:18080/__fixture/contracts")).json();
  expect(counterAfter.embedding_requests).toBe(counterBefore.embedding_requests);
  const empty = await call("POST", "/api/rag/search", { ...input, metadata: { environment: "development" } });
  expect(empty.status).toBe(200);
  expect(empty.body.items).toEqual([]);
  expect(empty.body.no_match).toBe(true);
  await page.reload();
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const after = await call("POST", "/api/rag/search", input);
  expect(after.status).toBe(200);
  expect(after.body.items.map((item: { document: { id: string } }) => item.document.id)).toEqual([corpus[0].created.body.id]);
  await testInfo.attach("typed-metadata-filter-evidence.json", { body: JSON.stringify({ schema: "typed-metadata-browser-v1", workspace, corpus, input,
    before, invalid, empty, after, embedding_requests_before_invalid: counterBefore.embedding_requests, embedding_requests_after_invalid: counterAfter.embedding_requests,
    limitations: ["browser -> production Go composition -> disposable Postgres, controlled embeddings", "API contract gate, not new filter UI", "no runtime filter propagation, ACL, expiry or live retrieval quality claim"] }, null, 2), contentType: "application/json" });
});
