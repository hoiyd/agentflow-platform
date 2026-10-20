import { expect, test } from "@playwright/test";
import { readFile } from "node:fs/promises";
import path from "node:path";

// Real browser -> production Go composition -> disposable Postgres. Fixtures
// assert filters and lifecycle, not just tags or a mocked successful API reply.
test("metadata Golden cases retain constraints and Context evidence across updates", async ({ page }, testInfo) => {
  const api = "http://127.0.0.1:18080";
  const root = path.resolve("../../examples/knowledge/metadata-golden-v1");
  const dataset = JSON.parse(await readFile(path.join(root, "dataset.json"), "utf8"));
  const manifest = JSON.parse(await readFile(path.join(root, "corpus-manifest.json"), "utf8"));
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const name = `Metadata Golden ${testInfo.repeatEachIndex}`;
  const created = await page.request.post(`${api}/api/workspaces`, { headers: { Origin: "http://127.0.0.1:13000" }, data: { name } });
  expect(created.status()).toBe(201);
  const workspace = (await created.json()).id;
  const headers = { "X-Workspace-ID": workspace, Origin: "http://127.0.0.1:13000" };
  const documents: Record<string, { id: string; updated_at: string; metadata: Record<string, unknown>; metadata_schema_version?: string }> = {};
  for (const document of manifest.documents) {
    const { file, ...input } = document;
    const response = await page.request.post(`${api}/api/documents`, { headers, data: { ...input, source_key: file, source_uri: file,
      source_type: "markdown", mime_type: "text/markdown", version: manifest.version, content: await readFile(path.join(root, file), "utf8") } });
    expect(response.status()).toBe(201);
    documents[file] = await response.json();
  }
  await page.reload();
  await page.getByRole("button", { name: /^Workspace: / }).click();
  await page.getByRole("option", { name, exact: true }).click();
  await expect(page.getByRole("button", { name: `Workspace: ${name}`, exact: true })).toBeVisible();
  await page.goto("/evaluations/retrieval");
  await page.getByRole("textbox", { name: "Golden dataset" }).fill(JSON.stringify(dataset, null, 2));
  const run = async () => {
    const pending = page.waitForResponse(response => response.url().endsWith("/api/rag/evaluations/run") && response.request().method() === "POST");
    await page.getByRole("button", { name: "Run evaluation", exact: true }).click();
    return pending;
  };
  const evaluated = await run();
  expect(evaluated.status()).toBe(200);
  const initial = await evaluated.json();
  expect(initial.summary.misses).toBe(0);
  expect(initial.cases).toHaveLength(dataset.cases.length);
  for (const item of initial.cases) {
    expect(item.hit).toBe(true);
    expect(item.evidence.context_leak_count).toBe(0);
    expect(item.evidence.schema_version).toBe("knowledge-metadata-v1");
    expect(item.evidence.workspace_id).toBe(workspace);
    for (const source of item.evidence.context_sources) {
      expect(source.document_id).toBe(documents[source.source_uri].id);
      expect(source.updated_at).toBeTruthy();
      expect(source.chunk_hash).toBeTruthy();
    }
  }
  expect(initial.cases[0].evidence.context_selection.parent_chunks + initial.cases[0].evidence.context_selection.adjacent_chunks).toBeGreaterThan(0);
  expect(initial.cases[0].evidence.context_sources[0].source_chunk_ids.length).toBeGreaterThan(1);
  await expect(page.getByText("Eligible source recall", { exact: true })).toBeVisible();
  const first = page.locator(".evaluation-case").first();
  await first.getByText("Applied constraints and source revisions", { exact: true }).click();
  await expect(first.locator("pre")).toContainText('"product": "Atlas"');
  await page.screenshot({ path: testInfo.outputPath("metadata-golden-evidence.png") });
  // Owner metadata updates change eligibility immediately, without re-ingestion.
  const atlas = documents["atlas-production.md"];
  const patch = await page.request.patch(`${api}/api/documents/${atlas.id}/metadata`, { headers, data: {
    expected_updated_at: atlas.updated_at, metadata_schema_version: atlas.metadata_schema_version,
    metadata: { ...atlas.metadata, environment: "staging" }
  } });
  expect(patch.status()).toBe(200);
  const updated = await patch.json();
  await page.reload();
  const noMatch = { ...dataset, cases: [{ ...dataset.cases[0], answerable: false, expected_sources: [],
    forbidden_sources: [...dataset.cases[0].forbidden_sources, ...dataset.cases[0].expected_sources] }] };
  await page.getByRole("textbox", { name: "Golden dataset" }).fill(JSON.stringify(noMatch));
  const changed = await run();
  expect(changed.status()).toBe(200);
  const afterUpdate = await changed.json();
  expect(afterUpdate.cases[0].hit).toBe(true);
  expect(afterUpdate.cases[0].items).toEqual([]);
  await expect(page.getByText("correct no-answer", { exact: true })).toBeVisible();
  // A typo/omission must be rejected, never converted into an unfiltered search.
  const missing = { ...dataset, cases: [{ ...dataset.cases[0], metadata: undefined }] };
  await page.getByRole("textbox", { name: "Golden dataset" }).fill(JSON.stringify(missing));
  const rejected = await run();
  expect(rejected.status()).toBe(400);
  await expect(page.locator(".rag-evaluation").getByRole("alert")).toContainText("requires a nonempty filter");
  const invalid = await page.request.post(`${api}/api/rag/evaluations/run`, { headers, data: {
    cases: [{ id: "null-filter", query: "ROLLBACK-42", metadata_filter: null }]
  } });
  expect(invalid.status()).toBe(400);
  expect(errors).toEqual([]);
  await testInfo.attach("metadata-golden-browser.json", { body: JSON.stringify({ schema: "metadata-golden-browser-v1", workspace,
    dataset, corpus_manifest: manifest, initial, updated, after_update: afterUpdate, rejected_status: rejected.status(),
    limitations: ["production API and Postgres; controlled embedding transport", "retrieval/Context regression, not live answer quality or Document ACL"] }, null, 2), contentType: "application/json" });
});
