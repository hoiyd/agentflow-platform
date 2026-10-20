import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

// Failure inventory: withdrawal must affect unfiltered search after reload;
// stale edits must retain the draft and show an actionable conflict, not clobber
// a newer document. Exercise the real UI, API, index and disposable Postgres.
test("Document lifecycle edits are durable, conditional and gate retrieval", async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace, Origin: "http://127.0.0.1:13000" };
  const api = "http://127.0.0.1:18080";
  const created = await page.request.post(`${api}/api/documents`, { headers, data: {
    title: `Lifecycle release guide ${testInfo.repeatEachIndex}`, source_key: `lifecycle-browser-${testInfo.repeatEachIndex}`,
    content: "ROLLBACK-742 LIFECYCLE-842 release guide preserves durable checkpoints.", metadata: { source: "synthetic" }
  } });
  expect(created.status()).toBe(201);
  const original = await created.json();
  await createWorkspaceChatAgent(page, workspace, `Lifecycle reader ${testInfo.repeatEachIndex}`, { retrieval_enabled: true, tools: ["knowledge_search", "knowledge_read"] });
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("rag-filter-gate ROLLBACK-742");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  const trace = page.getByRole("link", { name: "View trace", exact: true });
  await expect(trace).toBeVisible();
  const runID = (await trace.getAttribute("href"))!.split("/").at(-1)!;
  await expect.poll(async () => (await (await page.request.get(`${api}/api/runs/${runID}`, { headers })).json()).status).toBe("completed");
  const replayBefore = await (await page.request.get(`${api}/api/runs/${runID}/replay`, { headers })).json();
  expect(replayBefore.runtime_snapshot.knowledge_scope).toBeUndefined();
  expect(replayBefore.run_events.some((event: { type: string; payload: { tool_name?: string } }) => event.type === "tool.completed" && event.payload.tool_name === "knowledge_read")).toBe(true);
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: /^Documents/ }).click();
  const row = page.locator(".document-card").filter({ has: page.getByRole("heading", { name: original.title, exact: true }) });
  await row.getByRole("button", { name: "Details", exact: true }).click();
  const lifecycle = page.getByRole("region", { name: "Document lifecycle", exact: true });
  await expect(lifecycle).toBeVisible();
  await lifecycle.getByRole("button", { name: "Status: Active", exact: true }).click();
  await page.getByRole("option", { name: "Withdrawn", exact: true }).click();
  const saved = page.waitForResponse(response => response.request().method() === "PATCH" && response.url().endsWith(`/documents/${original.id}/metadata`));
  await lifecycle.getByRole("button", { name: "Save lifecycle", exact: true }).click();
  const withdrawn = await (await saved).json();
  expect(withdrawn.lifecycle.status).toBe("withdrawn");
  expect(withdrawn.embedding_count).toBe(original.embedding_count);
  const search = async () => {
    const response = await page.request.post(`${api}/api/rag/search`, { headers, data: { query: "LIFECYCLE-842" } });
    expect(response.status()).toBe(200);
    return response.json();
  };
  expect((await search()).items).toEqual([]);
  const replayAfter = await page.request.get(`${api}/api/runs/${runID}/replay`, { headers });
  expect(replayAfter.status()).toBe(200);
  expect((await replayAfter.json()).run_events).toEqual(replayBefore.run_events);
  await page.reload();
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: /^Documents/ }).click();
  await row.getByRole("button", { name: "Details", exact: true }).click();
  await expect(lifecycle.getByRole("button", { name: "Status: Withdrawn", exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("document-lifecycle.png") });
  // Another editor changes the current row while this editor retains its token.
  const restored = await page.request.patch(`${api}/api/documents/${original.id}/metadata`, { headers, data: {
    expected_updated_at: withdrawn.updated_at, metadata: withdrawn.metadata, lifecycle: { status: "active" }
  } });
  expect(restored.status()).toBe(200);
  await lifecycle.getByRole("button", { name: "Status: Withdrawn", exact: true }).click();
  await page.getByRole("option", { name: "Draft", exact: true }).click();
  await lifecycle.getByRole("button", { name: "Save lifecycle", exact: true }).click();
  await expect(lifecycle.getByRole("alert")).toContainText("document changed");
  await expect(lifecycle.getByRole("button", { name: "Status: Draft", exact: true })).toBeVisible();
  await lifecycle.getByRole("button", { name: "Reload document", exact: true }).click();
  await expect(lifecycle.getByRole("button", { name: "Status: Active", exact: true })).toBeVisible();
  await lifecycle.getByLabel("Valid until (UTC)", { exact: true }).fill("2000-01-01T00:00");
  const expiredResponse = page.waitForResponse(response => response.request().method() === "PATCH" && response.url().endsWith(`/documents/${original.id}/metadata`));
  await lifecycle.getByRole("button", { name: "Save lifecycle", exact: true }).click();
  const expired = await (await expiredResponse).json();
  expect(expired.lifecycle.valid_until).toBe("2000-01-01T00:00:00Z");
  expect((await search()).items).toEqual([]);
  await expect(lifecycle.getByRole("button", { name: "Save lifecycle", exact: true })).toBeDisabled();
  await expect(lifecycle).toContainText("Expired");
  expect(errors).toEqual([]);
  await testInfo.attach("document-lifecycle-evidence.json", { body: JSON.stringify({ schema: "document-lifecycle-browser-v1", workspace, original, withdrawn, expired, run_id: runID,
    outcomes: ["withdrawal persists across reload", "unfiltered recall excludes withdrawn/expired rows", "stale write rejected without discarding draft", "embedding count unchanged"],
    limitations: ["synthetic documents; production Go API + disposable Postgres", "controlled embedding transport; no live model quality claim"] }, null, 2), contentType: "application/json" });
});

test("A delayed lifecycle save cannot replace another selected document", async ({ page }, testInfo) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace, Origin: "http://127.0.0.1:13000" };
  const documents = [];
  for (const name of ["Delayed lifecycle", "New selection"]) {
    const title = `${name} ${testInfo.repeatEachIndex}`;
    const response = await page.request.post("http://127.0.0.1:18080/api/documents", { headers, data: { title, content: `${title} synthetic content.` } });
    expect(response.status()).toBe(201);
    documents.push(await response.json());
  }
  await page.reload();
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: /^Documents/ }).click();
  const row = (title: string) => page.locator(".document-card").filter({ has: page.getByRole("heading", { name: title, exact: true }) });
  await row(documents[0].title).getByRole("button", { name: "Details", exact: true }).click();
  const lifecycle = page.getByRole("region", { name: "Document lifecycle", exact: true });
  await lifecycle.getByRole("button", { name: "Status: Active", exact: true }).click();
  await page.getByRole("option", { name: "Draft", exact: true }).click();
  let release!: () => void;
  let arrived!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  const ready = new Promise<void>(resolve => { arrived = resolve; });
  let finished!: () => void;
  const delivered = new Promise<void>(resolve => { finished = resolve; });
  await page.route(`**/api/documents/${documents[0].id}/metadata`, async route => {
    const response = await route.fetch();
    arrived();
    await held;
    await route.fulfill({ response });
    finished();
  }, { times: 1 });
  await lifecycle.getByRole("button", { name: "Save lifecycle", exact: true }).click();
  await ready;
  await row(documents[1].title).getByRole("button", { name: "Details", exact: true }).click();
  await expect(row(documents[1].title).getByRole("region", { name: "Document lifecycle", exact: true })).toBeVisible();
  release();
  await delivered;
  await expect(row(documents[1].title).getByRole("button", { name: "Status: Active", exact: true })).toBeVisible();
  await expect(row(documents[0].title).getByRole("region", { name: "Document lifecycle", exact: true })).toHaveCount(0);
  const persisted = await (await page.request.get(`http://127.0.0.1:18080/api/documents/${documents[0].id}`, { headers })).json();
  expect(persisted.document.lifecycle.status).toBe("draft");
  await testInfo.attach("delayed-lifecycle-evidence.json", { body: JSON.stringify({ schema: "document-lifecycle-navigation-v1", workspace, documents, persisted: persisted.document,
    outcome: "write persists without stealing the new selection", limitations: ["controlled network delay; real API and disposable Postgres"] }, null, 2), contentType: "application/json" });
});
