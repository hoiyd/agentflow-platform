import { expect, test } from "@playwright/test";
import { defaultWorkspaceID } from "./fixtures/workspace-agent";

test("versioned metadata validates ownership and survives update, retrieval and reload", async ({ page, request }, testInfo) => {
  const api = "http://127.0.0.1:18080";
  const version = "knowledge-metadata-v1";
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const call = (method: string, path: string, body?: unknown) => page.evaluate(async input => {
    const response = await fetch(`http://127.0.0.1:18080${input.path}`, {
      method: input.method, credentials: "include",
      headers: { "Content-Type": "application/json", "X-Workspace-ID": input.workspace },
      body: typeof input.body === "string" ? input.body : input.body === undefined ? undefined : JSON.stringify(input.body)
    });
    return { status: response.status, body: await response.json() };
  }, { path, method, body, workspace });
  const schema = await call("GET", "/api/documents/metadata-schema");
  expect(schema.status).toBe(200);
  expect(schema.body.version).toBe(version);
  expect(schema.body.fields).toEqual(expect.arrayContaining([
    expect.objectContaining({ key: "product", type: "string", owner: "owner_declared" }),
    expect.objectContaining({ key: "environment", type: "enum", owner: "owner_declared" }),
    expect.objectContaining({ key: "chunk_type", owner: "system_derived", scope: "chunk" })
  ]));
  const input = { title: `Schema release guide ${testInfo.repeatEachIndex + 1}`, source_key: `schema-release-${testInfo.repeatEachIndex}`, source_type: "markdown",
    content: "# AgentFlow Release\n\nDeploy AgentFlow production release safely.\n\n## Recovery\n\nRollback AgentFlow production release.",
    metadata_schema_version: version, metadata: { product: "AgentFlow", environment: "production", language: "en", tags: ["release", "recovery"], priority: 2.5,
      is_reference: true, published_at: "2026-10-18T12:00:00+08:00", extra: { custom: "retained" }, access_scope: "public" } };
  const created = await call("POST", "/api/documents", input);
  expect(created.status).toBe(201);
  expect(created.body.metadata_schema_version).toBe(version);
  expect(created.body.metadata.published_at).toBe("2026-10-18T04:00:00Z");
  const detailPath = `/api/documents/${created.body.id}`;
  const before = await call("GET", detailPath);
  const counter = await (await request.get(`${api}/__fixture/contracts`)).json();
  const failures = [];
  for (const bad of [
    { metadata_schema_version: "unknown-v2" },
    { metadata: { product: null } }, { metadata: { environment: "made-up" } },
    { metadata: { tags: ["valid", 1] } }, { metadata: { priority: "2" } },
    { metadata: { is_reference: "true" } }, { metadata: { published_at: "yesterday" } },
    { metadata: { product: "x".repeat(121) } }, { metadata: { workspace_id: "foreign" } },
    { metadata: { chunk_index: 99 } }
  ]) {
    const result = await call("POST", "/api/documents", { ...input, ...bad });
    expect(result.status).toBe(400);
    failures.push({ bad, result });
  }
  for (const raw of [
    `{"title":"first","title":"second","content":"release","metadata_schema_version":"${version}"}`,
    `{"title":"release","content":"release","metadata_schema_version":"${version}","metadata":{"priority":100.00000000000000001}}`,
    `{"title":"release","content":"release","metadata_schema_version":"${version}","metadata":{"priority":-1e-9999}}`,
    `{"title":"release","content":"release","metadata_schema_version":"${version}","metadata":{"extra":{"key":1,"key":2}}}`
  ]) {
    const result = await call("POST", "/api/documents", raw);
    expect(result.status).toBe(400);
    failures.push({ raw, result });
  }
  const rejected = await call("PATCH", `${detailPath}/metadata`, { expected_updated_at: created.body.updated_at, metadata: { priority: "bad" } });
  expect(rejected.status).toBe(400);
  expect(await call("GET", detailPath)).toEqual(before);
  const patched = await call("PATCH", `${detailPath}/metadata`, { expected_updated_at: created.body.updated_at, metadata: { ...input.metadata, priority: 3 } });
  expect(patched.status).toBe(200);
  expect(patched.body.metadata_schema_version).toBe(version);
  const afterCounter = await (await request.get(`${api}/__fixture/contracts`)).json();
  expect(afterCounter.embedding_requests).toBe(counter.embedding_requests);
  const search = await call("POST", "/api/rag/search", { query: "AgentFlow production release", metadata: { product: "AgentFlow" }, limit: 10 });
  expect(search.status).toBe(200);
  expect(search.body.items.some((item: { document: { id: string; metadata_schema_version: string } }) => item.document.id === created.body.id && item.document.metadata_schema_version === version)).toBe(true);
  await page.reload();
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: /^Documents/ }).click();
  const card = page.locator(".document-card").filter({ has: page.getByRole("heading", { name: input.title, exact: true }) });
  await card.getByRole("button", { name: "Details", exact: true }).click();
  await card.getByText(`Document metadata · ${version}`, { exact: true }).click();
  await expect(card.getByRole("table", { name: "Document metadata" })).toContainText("Owner-declared");
  await expect(card.getByRole("table", { name: "Document metadata" })).toContainText("Unverified extension");
  await page.screenshot({ path: testInfo.outputPath("metadata-schema.png") });
  const after = await call("GET", detailPath);
  expect(after.body.document.metadata_schema_version).toBe(version);
  expect(after.body.chunks.map((chunk: { id: string }) => chunk.id)).toEqual(before.body.chunks.map((chunk: { id: string }) => chunk.id));
  expect(after.body.chunks.every((chunk: { metadata: { priority: number } }) => chunk.metadata.priority === 3)).toBe(true);
  expect(errors).toEqual([]);
  await testInfo.attach("metadata-schema-evidence.json", { body: JSON.stringify({ schema: "metadata-schema-browser-v1", workspace, schema_contract: schema.body, input, created, before, failures, rejected, patched, after, search,
    embedding_requests_before: counter.embedding_requests, embedding_requests_after: afterCounter.embedding_requests,
    limitations: ["controlled embeddings, not live quality", "legacy equality filters, not typed filters or expiry", "owner declarations are not trusted ACLs"] }, null, 2), contentType: "application/json" });
});
