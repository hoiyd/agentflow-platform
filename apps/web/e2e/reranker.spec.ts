import { test, expect } from "@playwright/test";

const api = "http://127.0.0.1:18080";
test.skip(process.env.AGENTFLOW_RERANKER_TEST !== "1", "requires controlled TEI fixture");

test("cross-encoder search, safe failures, and persisted index survive reload", async ({ page, request }) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: /^Documents/ }).click();
  await page.getByRole("tab", { name: "Paste text", exact: true }).click();
  await page.getByRole("textbox", { name: "Document title", exact: true }).fill("Cross-encoder recovery guide");
  await page.getByRole("textbox", { name: "Document content", exact: true }).fill("The alpha-4242 recovery protocol requires restarting the coordinator and validating its trace.");
  const created = page.waitForResponse(response => response.url() === `${api}/api/documents` && response.request().method() === "POST");
  await page.getByRole("button", { name: "Add document", exact: true }).click();
  const creation = await created;
  expect(creation.status()).toBe(201);
  const document = await creation.json();
  await page.getByRole("tab", { name: "Search", exact: true }).click();
  const evidence: unknown[] = [];
  async function search(query: string, status: number) {
    await page.getByRole("textbox", { name: "Search indexed knowledge" }).fill(query);
    const pending = page.waitForResponse(response => response.url() === `${api}/api/rag/search` && response.request().method() === "POST");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    const response = await pending;
    expect(response.status()).toBe(status);
    const body = await response.json();
    evidence.push({ query, status, body });
    return body;
  }
  const successful = await search("alpha-4242 recovery protocol", 200);
  expect(successful.reranker).toMatchObject({ algorithm: "cross_encoder", provider: "tei", model: "fixture-cross-encoder" });
  expect(successful.items.map((item: { document: { id: string } }) => item.document.id)).toContain(document.id);
  await expect(page.locator(".rag-results:not(.compact-results)")).toContainText("Cross-encoder recovery guide");
  await page.locator(".retrieval-diagnostics summary").click();
  await expect(page.locator(".retrieval-diagnostics")).toContainText("tei / fixture-cross-encoder");

  for (const [query, status, code] of [
    ["alpha-4242 reranker-failure", 502, "reranker_unavailable"],
    ["alpha-4242 reranker-timeout", 504, "reranker_timeout"]
  ] as const) {
    const failed = await search(query, status);
    expect(failed).toMatchObject({ source: "reranker", code });
    expect(JSON.stringify(failed)).not.toContain("PRIVATE_RERANKER_RESPONSE");
    await expect(page.locator(".knowledge-error")).toContainText(code);
    await expect(page.locator(".rag-results:not(.compact-results)")).not.toContainText("Cross-encoder recovery guide");
    await expect(page.getByRole("button", { name: "Search", exact: true })).toBeEnabled();
  }
  await page.reload();
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: "Search", exact: true }).click();
  const reloaded = await search("alpha-4242 recovery protocol", 200);
  expect(reloaded.items[0].document.id).toBe(document.id);
  expect(reloaded.reranker.config_version).toBe(successful.reranker.config_version);
  await expect(page.locator(".knowledge-error")).toHaveCount(0);
  await expect(page.locator(".rag-results:not(.compact-results)")).toContainText("Cross-encoder recovery guide");
  const contracts = await (await request.get(`${api}/__fixture/contracts`)).json();
  expect(contracts.failures ?? []).toEqual([]);
  expect(errors).toEqual([]);
  await test.info().attach("cross-encoder-evidence", {
    body: JSON.stringify({ documentId: document.id, evidence, contracts, limitations: "Controlled TEI protocol fixture, not live model quality or latency evidence; production composition and disposable Postgres." }),
    contentType: "application/json"
  });
});
