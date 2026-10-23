import { expect, test } from "@playwright/test";
import type { DocumentInfo } from "../lib/knowledge-api";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test("Knowledge scope flows from search and Chat into persisted Replay", async ({ page }, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const product = `Runtime scope ${testInfo.repeatEachIndex}`;
  const headers = { "X-Workspace-ID": workspace, Origin: "http://127.0.0.1:13000" };
  const corpus: DocumentInfo[] = [];
  for (const allowed of [true, false]) {
    const input = { title: allowed ? product : `${product} decoy`,
      content: `ROLLBACK-742 scoped recovery preserves durable state.${allowed ? "" : " FORBIDDEN_SCOPE_CONTENT"}`,
      metadata_schema_version: "knowledge-metadata-v1", metadata: { product: allowed ? product : "Other", environment: "production", language: "en" } };
    const response = await page.request.post("http://127.0.0.1:18080/api/documents", { headers, data: input });
    expect(response.status()).toBe(201);
    corpus.push(await response.json());
  }
  await createWorkspaceChatAgent(page, workspace, `Runtime reader ${testInfo.repeatEachIndex}`, { retrieval_enabled: true, tools: ["knowledge_search", "knowledge_read"] });
  await page.getByRole("button", { name: "Knowledge scope for new runs", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Knowledge scope", exact: true });
  await dialog.getByRole("textbox", { name: "Knowledge product", exact: true }).fill(product);
  await dialog.getByRole("button", { name: "Knowledge environment: All environments", exact: true }).click();
  await dialog.getByRole("option", { name: "production", exact: true }).click();
  await dialog.getByRole("textbox", { name: "Knowledge language", exact: true }).fill("en");
  const applyBox = await dialog.getByRole("button", { name: "Apply scope", exact: true }).boundingBox();
  const cancelBox = await dialog.getByRole("button", { name: "Cancel", exact: true }).boundingBox();
  expect(applyBox?.height).toBe(cancelBox?.height);
  await page.screenshot({ path: testInfo.outputPath("knowledge-scope-dialog.png") });
  await dialog.getByRole("button", { name: "Apply scope", exact: true }).click();

  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: "Search", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Knowledge product", exact: true })).toHaveValue(product);
  await page.getByRole("textbox", { name: "Search indexed knowledge", exact: true }).fill("ROLLBACK-742");
  const searched = page.waitForResponse(response => response.url().endsWith("/api/rag/search") && response.request().method() === "POST");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const search = await (await searched).json();
  expect(search.items.map((item: { document: { id: string } }) => item.document.id)).toEqual([corpus[0].id]);
  await expect(page.locator(".rag-results").first()).toContainText(product);
  await expect(page.locator(".rag-results").first()).not.toContainText("FORBIDDEN_SCOPE_CONTENT");
  await page.screenshot({ path: testInfo.outputPath("knowledge-filter-search.png") });
  // Hold the real response, then change scope. Late results must not reappear.
  let release!: () => void;
  let arrived!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  const ready = new Promise<void>(resolve => { arrived = resolve; });
  await page.route("**/api/rag/search", async route => {
    const response = await route.fetch();
    arrived();
    await held;
    await route.fulfill({ response });
  }, { times: 1 });
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await ready;
  await page.getByRole("textbox", { name: "Knowledge product", exact: true }).fill("Other");
  release();
  await expect(page.locator(".rag-results").first()).not.toContainText(product);
  await expect(page.getByRole("button", { name: "Search", exact: true })).toBeEnabled();
  await page.getByRole("textbox", { name: "Knowledge product", exact: true }).fill(product);
  await page.reload();
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: "Search", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Knowledge product", exact: true })).toHaveValue(product);

  await page.getByRole("button", { name: "Chat", exact: true }).click();
  await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await page.getByRole("button", { name: /^Agent: / }).click();
  await page.getByRole("option", { name: `Runtime reader ${testInfo.repeatEachIndex}`, exact: true }).click();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("rag-filter-gate ROLLBACK-742");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  const trace = page.getByRole("link", { name: "View trace", exact: true });
  await expect(trace).toBeVisible();
  const href = (await trace.getAttribute("href"))!;
  const runID = href.split("/").at(-1)!;
  await expect.poll(async () => (await (await page.request.get(`http://127.0.0.1:18080/api/runs/${runID}`, { headers })).json()).status).toBe("completed");
  const readReplay = async () => {
    const response = await page.request.get(`http://127.0.0.1:18080/api/runs/${runID}/replay`, { headers });
    expect(response.status()).toBe(200);
    return response.json();
  };
  const before = await readReplay();
  const frozen = before.runtime_snapshot.knowledge_scope;
  expect(frozen.filter.where.all).toContainEqual({ field: "document.metadata.product", op: "eq", value: product });
  expect(before.run_events.some((event: { type: string; payload: { tool_name?: string } }) => event.type === "tool.completed" && event.payload.tool_name === "knowledge_read")).toBe(true);
  await page.getByRole("button", { name: "Knowledge scope for new runs", exact: true }).click();
  await dialog.getByRole("button", { name: "Clear", exact: true }).click();
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("button", { name: "Knowledge scope for new runs", exact: true }).click();
  await expect(dialog.getByRole("textbox", { name: "Knowledge product", exact: true })).toHaveValue(product);
  await dialog.getByRole("button", { name: "Clear", exact: true }).click();
  await dialog.getByRole("button", { name: "Apply scope", exact: true }).click();
  await page.getByRole("button", { name: "Knowledge", exact: true }).click();
  await page.getByRole("tab", { name: "Search", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Knowledge product", exact: true })).toHaveValue("");
  await page.getByRole("textbox", { name: "Search indexed knowledge", exact: true }).fill("ROLLBACK-742");
  const clearedRequest = page.waitForResponse(response => response.url().endsWith("/api/rag/search") && response.request().method() === "POST");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const clearedResponse = await clearedRequest;
  expect(clearedResponse.request().postDataJSON().metadata_filter).toBeUndefined();
  const cleared = await clearedResponse.json();
  expect(cleared.items.some((item: { document: { id: string } }) => item.document.id === corpus[1].id)).toBe(true);
  await page.getByRole("button", { name: "Chat", exact: true }).click();
  await trace.click();
  await page.getByText("Saved Knowledge scope", { exact: true }).click();
  await expect(page.locator(".frozen-knowledge-scope")).toContainText(product);
  await page.screenshot({ path: testInfo.outputPath("knowledge-scope-replay.png") });
  await page.reload();
  await page.getByText("Saved Knowledge scope", { exact: true }).click();
  await expect(page.locator(".frozen-knowledge-scope")).toContainText(product);
  const after = await readReplay();
  expect(after.runtime_snapshot.knowledge_scope).toEqual(frozen);
  // A queued follow-up inherits its source Run, not the cleared browser draft.
  const inputsPath = `http://127.0.0.1:18080/api/conversations/${before.run.conversation_id}/inputs`;
  const queued = await page.request.post(inputsPath, { headers, data: { kind: "follow_up", run_id: runID,
    mode: "single", agent_id: before.run.agent_id, content: "rag-filter-gate ROLLBACK-742", idempotency_key: "scope-followup" } });
  expect(queued.status()).toBe(202);
  const receipt = await queued.json();
  let appliedID = "";
  await expect.poll(async () => {
    const receipts = await (await page.request.get(inputsPath, { headers })).json();
    appliedID = receipts.find((item: { id: string }) => item.id === receipt.id)?.applied_run_id ?? "";
    return appliedID;
  }).not.toBe("");
  await expect.poll(async () => (await (await page.request.get(`http://127.0.0.1:18080/api/runs/${appliedID}`, { headers })).json()).status).toBe("completed");
  const followup = await (await page.request.get(`http://127.0.0.1:18080/api/runs/${appliedID}/replay`, { headers })).json();
  expect(followup.runtime_snapshot.knowledge_scope).toEqual(frozen);
  expect(errors).toEqual([]);
  await testInfo.attach("runtime-filter-evidence.json", { body: JSON.stringify({ schema: "runtime-filter-browser-v1", workspace, corpus, search, run_id: runID,
    frozen_scope: frozen, reloaded_scope: after.runtime_snapshot.knowledge_scope, cleared_search: cleared, followup_run: appliedID, followup_scope: followup.runtime_snapshot.knowledge_scope,
    limitations: ["production Go + disposable Postgres; controlled model/embedding transport", "business filter, not Document ACL or live quality evidence"] }, null, 2), contentType: "application/json" });
});

test("Knowledge drafts stay in their owning Workspace", async ({ page }, testInfo) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const original = await page.getByRole("button", { name: /^Workspace: / }).getAttribute("aria-label");
  await page.getByRole("button", { name: "Knowledge scope for new runs", exact: true }).click();
  await page.getByRole("textbox", { name: "Knowledge product", exact: true }).fill("Workspace-local product");
  await page.getByRole("button", { name: "Apply scope", exact: true }).click();
  const name = `Scope isolation ${testInfo.repeatEachIndex}`;
  const created = await page.request.post("http://127.0.0.1:18080/api/workspaces", { headers: { Origin: "http://127.0.0.1:13000" }, data: { name } });
  expect(created.status()).toBe(201);
  await page.reload();
  await page.getByRole("button", { name: /^Workspace: / }).click();
  // Workspace selection reloads the document. Never open a dialog on the old
  // page while its replacement is still loading.
  await Promise.all([
    page.waitForEvent("domcontentloaded"),
    page.getByRole("option", { name, exact: true }).click()
  ]);
  await expect(page.getByRole("button", { name: `Workspace: ${name}`, exact: true })).toBeVisible();
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Knowledge scope for new runs", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Knowledge product", exact: true })).toHaveValue("");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: "Knowledge scope for new runs", exact: true })).toBeFocused();
  await page.getByRole("button", { name: /^Workspace: / }).click();
  await Promise.all([
    page.waitForEvent("domcontentloaded"),
    page.getByRole("option", { name: original!.replace(/^Workspace: /, ""), exact: true }).click()
  ]);
  await expect(page.getByRole("button", { name: original!, exact: true })).toBeVisible();
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Knowledge scope for new runs", exact: true }).click();
  await expect(page.getByRole("textbox", { name: "Knowledge product", exact: true })).toHaveValue("Workspace-local product");
  await testInfo.attach("workspace-scope-evidence.json", { body: JSON.stringify({ original_workspace: workspace, isolated_workspace: (await created.json()).id, restored_product: "Workspace-local product" }), contentType: "application/json" });
});

test("older backends cannot silently ignore a selected Run scope", async ({ page }, testInfo) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  await createWorkspaceChatAgent(page, workspace, `Scope compatibility ${testInfo.repeatEachIndex}`);
  await page.getByRole("button", { name: "Knowledge scope for new runs", exact: true }).click();
  await page.getByRole("textbox", { name: "Knowledge product", exact: true }).fill("AgentFlow");
  await page.getByRole("button", { name: "Apply scope", exact: true }).click();
  await page.route("**/api/documents/metadata-schema", route => route.fulfill({ json: { version: "knowledge-metadata-v1", typed_filtering: true } }));
  let sent = 0;
  page.on("request", request => { if (request.method() === "POST" && request.url().endsWith("/api/chat")) sent++; });
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("Must not run without the selected filter");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByText(/Backend cannot enforce the saved Knowledge scope/)).toBeVisible();
  expect(sent).toBe(0);
});
