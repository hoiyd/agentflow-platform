import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent } from "./fixtures/workspace-agent";

// Failure inventory: Composer geometry must not resize shared actions; disabled
// controls, keyboard focus and real Knowledge/Memory requests must still work.
test("shared primary actions keep local geometry across workbench views", async ({ page }, info) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  const created = await page.request.post("http://127.0.0.1:18080/api/workspaces", {
    headers: { Origin: "http://127.0.0.1:13000" }, data: { name: "Shared action evidence" }
  });
  expect(created.status()).toBe(201);
  const workspace: string = (await created.json()).id;
  await page.reload();
  await page.getByRole("button", { name: /^Workspace: / }).click();
  await page.getByRole("option", { name: "Shared action evidence", exact: true }).click();
  await expect(page.getByRole("button", { name: "Workspace: Shared action evidence", exact: true })).toBeVisible();
  const agent = await createWorkspaceChatAgent(page, workspace, "Workbench reader");
  const measurements: Record<string, string> = {};
  const capture = async (name: string) => info.attach(`${name}.png`, { body: await page.screenshot(), contentType: "image/png" });
  try {
    const send = page.getByRole("button", { name: "Send message", exact: true });
    await expect(send).toBeDisabled();
    await expect(send).toHaveCSS("height", "44px");
    measurements.composer = await send.evaluate(el => getComputedStyle(el).height);
    await page.getByRole("button", { name: "Configure", exact: true }).click();
    const saveConfig = page.locator(".agent-config-actions .send");
    await expect(saveConfig).toHaveCSS("min-height", "42px");
    measurements.agent = await saveConfig.evaluate(el => getComputedStyle(el).height);
    await capture("agent-config-actions-desktop");
    await page.getByRole("button", { name: /^Cancel / }).click();

    await page.getByRole("button", { name: "Knowledge", exact: true }).click();
    await page.getByRole("tab", { name: /^Search/ }).click();
    const search = page.getByRole("button", { name: "Search", exact: true });
    await expect(search).toBeDisabled();
    await page.getByRole("textbox", { name: "Search indexed knowledge", exact: true }).fill("Synthetic unmatched workbench query");
    await expect(search).toBeEnabled();
    await expect(search).toHaveCSS("min-height", "42px");
    await search.focus();
    await search.press("Shift+Tab");
    await page.keyboard.press("Tab");
    await expect(search).toBeFocused();
    await expect(search).toHaveCSS("outline-style", "solid");
    const response = page.waitForResponse(r => r.request().method() === "POST" && r.url().endsWith("/api/rag/search"));
    await search.click();
    expect((await response).status()).toBe(200);
    await expect(page.getByText("No results passed the relevance gate.", { exact: true })).toBeVisible();
    measurements.knowledge = await search.evaluate(el => getComputedStyle(el).height);
    await capture("knowledge-actions-desktop");

    await page.getByRole("button", { name: "Memory", exact: true }).click();
    const saveMemory = page.getByRole("button", { name: "Save memory", exact: true });
    await expect(saveMemory).toBeDisabled();
    await expect(saveMemory).toHaveCSS("min-height", "42px");
    await page.getByPlaceholder("A durable fact, preference, correction, or project convention").fill("Synthetic workbench actions preserve source boundaries.");
    await saveMemory.click();
    await expect(page.getByText("Saved", { exact: true })).toBeVisible();
    measurements.memory = await saveMemory.evaluate(el => getComputedStyle(el).height);
    await capture("memory-actions-desktop");
    await page.reload();
    await page.getByRole("button", { name: "Memory", exact: true }).click();
    await page.getByRole("textbox", { name: "Memory search query", exact: true }).fill("Synthetic workbench actions");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.locator(".memory-result-main")).toContainText("Synthetic workbench actions preserve source boundaries.");
    expect(errors).toEqual([]);
    await info.attach("shared-actions-evidence.json", { body: JSON.stringify({ schema: "shared-actions-browser-v1", workspace, agent_id: agent.id, measurements,
      checks: ["local Composer geometry", "shared action height", "disabled state", "keyboard focus", "real RAG request", "Memory persistence and reload"],
      limitations: ["desktop only", "controlled provider and disposable Postgres", "not live model quality"] }, null, 2), contentType: "application/json" });
  } finally {
    await page.request.delete(`http://127.0.0.1:18080/api/agents/${agent.id}`, { headers: { "X-Workspace-ID": workspace } });
    await page.request.delete(`http://127.0.0.1:18080/api/workspaces/${workspace}`);
  }
});
