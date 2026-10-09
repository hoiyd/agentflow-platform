import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test.skip(process.env.AGENTFLOW_REASONING_TEST !== "1", "requires isolated reasoning fixture");
const api = "http://127.0.0.1:18080";

for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
test(`${mode}: committed answer and reasoning survive refresh, reset and cancellation without restarting the provider`, async ({ page, request }) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Create new agent" });
  const capability = `partial${mode.toLowerCase().replace(/[^a-z]/g, "")}${test.info().repeatEachIndex}`;
  await dialog.getByLabel("Name", { exact: true }).fill(`Partial recovery ${mode}`);
  await dialog.getByRole("textbox", { name: "Description", exact: true }).fill("partial recovery fixture specialist");
  await dialog.getByRole("textbox", { name: "System prompt", exact: true }).fill("Use the clock and explain evidence.");
  await dialog.getByText("Routing signals", { exact: true }).click();
  await dialog.getByRole("textbox", { name: "Capabilities", exact: true }).fill(capability);
  await dialog.getByRole("textbox", { name: "Example tasks", exact: true }).fill("reasoning-partial-gate checkpoint recovery");
  await dialog.getByLabel("Memory retrieval", { exact: true }).uncheck();
  await dialog.getByLabel("Knowledge retrieval", { exact: true }).uncheck();
  await dialog.getByRole("checkbox", { name: "get_current_time", exact: true }).check();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await page.getByRole("button", { name: "OK", exact: true }).click();
  await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("reasoning-partial-gate: checkpoint recovery");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  if (mode === "Multi-agent") {
    await page.getByText("Routing requirements", { exact: true }).click();
    await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill(capability);
    await page.getByRole("button", { name: "Approve & Continue" }).click();
  }
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  const id = href!.split("/").at(-1)!;
  const projection = async () => (await request.get(`${api}/api/runs/${id}/projection`)).json();
  await expect.poll(async () => (await projection()).partial_outputs.some((item: { text: string }) => item.text.includes("Recover this answer"))).toBe(true);
  const contracts = await (await request.get(`${api}/__fixture/contracts`)).json();
  await page.reload();
  const recovered = page.getByRole("region", { name: "Recovered output" });
  await expect(recovered).toContainText("Recover this answer");
  await expect(recovered).toContainText("DISPLAY_ONLY_REASONING");
  await expect(recovered).toContainText("Provisional");
  expect(await recovered.innerText()).not.toContain("sk-fixture");
  expect(await recovered.innerText()).not.toContain("Discard this draft");
  const after = await (await request.get(`${api}/__fixture/contracts`)).json();
  expect(after.requests).toBe(contracts.requests);
  await page.locator(".topbar").getByRole("button", { name: "Stop", exact: true }).click();
  await expect(page.getByLabel("Task status: canceled", { exact: true })).toBeVisible();
  await expect(recovered).toContainText("Incomplete");
  await page.reload();
  await expect(recovered).toContainText("Recover this answer");
  await expect(recovered).toContainText("Incomplete");
  const saved = await projection();
  expect(saved.partial_outputs.every((item: { status: string }) => item.status === "interrupted")).toBe(true);
  await test.info().attach("durable-partial-output-evidence", { body: JSON.stringify({
    mode, run_id: id, projection: saved, provider_requests_before_reload: contracts.requests,
    provider_requests_after_reload: after.requests, checks: ["answer reset", "refresh", "reasoning redaction", "cancellation", "durable replacement"],
    limitations: ["deterministic provider fixture, real browser and disposable Postgres", "no model request resumption"]
  }, null, 2), contentType: "application/json" });
});
}

test("successful completion replaces recovered output with one canonical answer", async ({ page, request }) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await createWorkspaceChatAgent(page, await defaultWorkspaceID(page), "Partial output finalization", { tools: ["get_current_time"] });
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("reasoning-partial-gate: finish recovery");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.locator(".message.assistant").last()).toContainText("Recover this answer");
  await page.reload();
  await expect(page.getByRole("region", { name: "Recovered output" })).toContainText("Recover this answer");
  expect((await request.post(`${api}/__fixture/release`)).status()).toBe(204);
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Recovered output" })).toHaveCount(0);
  await expect(page.locator(".message.assistant")).toHaveCount(1);
  await expect(page.locator(".message.assistant")).toContainText("finished.");
  await page.reload();
  await expect(page.locator(".message.assistant")).toHaveCount(1);
  await expect(page.getByRole("region", { name: "Recovered output" })).toHaveCount(0);
  await test.info().attach("partial-output-finalization-evidence", { body: JSON.stringify({
    canonical_answer_count: 1, recovered_display_removed: true, reload_verified: true,
    limitations: ["deterministic fixture, no live-model performance claims"]
  }), contentType: "application/json" });
});
