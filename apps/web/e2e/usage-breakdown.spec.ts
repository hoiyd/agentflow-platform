import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test.skip(process.env.AGENTFLOW_USAGE_TEST !== "1", "requires a frozen fixture quote");

// Browser -> production Go runtime -> disposable Postgres -> Replay/reload.
// No real credentials/provider charges; fixtures assert wire usage, not UI mocks.
for (const scenario of [
  { suffix: "", cached: 30, reasoning: 4, cost: 65, source: "openai_details" },
  { suffix: "usage-zero", cached: 0, reasoning: 4, cost: 110, source: "openai_details" },
  { suffix: "usage-unknown", cached: undefined, reasoning: undefined, cost: 110, source: undefined },
  { suffix: "usage-invalid", cached: undefined, reasoning: undefined, cost: 110, source: "invalid_details" }
]) {
  test(`usage breakdown ${scenario.suffix || "cache hit"}: persists and reloads`, async ({ page, request }) => {
    await page.goto("/workspace");
    await expect(page.getByText("API connected", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
    const workspace = await defaultWorkspaceID(page);
    const agent = await createWorkspaceChatAgent(page, workspace, `Usage ${scenario.suffix || "cache hit"}`);
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(`usage-breakdown ${scenario.suffix}: explain usage briefly`);
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    const href = (await page.getByRole("link", { name: "View trace" }).getAttribute("href"))!;
    const id = href.split("/").at(-1)!;
    const response = await request.get(`http://127.0.0.1:18080/api/runs/${id}/replay`);
    expect(response.ok()).toBe(true);
    const replay = await response.json();
    expect(replay.run.agent_id).toBe(agent.id);
    expect(replay.run.workspace_id).toBe(workspace);
    const ledger = replay.usage_ledger;
    const entries = ledger.entries.filter((entry: { kind: string }) => entry.kind === "model.settlement");
    expect(entries.length).toBeGreaterThan(0);
    for (const entry of entries) {
      expect(entry.breakdown?.cached_input_tokens).toBe(scenario.cached);
      expect(entry.breakdown?.reasoning_tokens).toBe(scenario.reasoning);
      expect(entry.breakdown?.source).toBe(scenario.source);
      expect(entry.total_tokens).toBe(60);
      expect(entry.estimated_cost_micros).toBe(scenario.cost);
      expect(entry.cost_details.pricing.source).toBe("fixture");
    }
    expect(ledger.totals.model_calls).toBe(entries.length);
    expect(ledger.totals.open_reservations).toBe(0);
    const attempts = replay.run_events.filter((event: { type: string }) => event.type === "model.attempt_finished");
    expect(attempts.length).toBeGreaterThan(0);
    expect(attempts.some((event: { payload: { breakdown?: { source: string } } }) => event.payload.breakdown?.source === scenario.source)).toBe(true);
    await page.goto(href);
    await page.getByText("Model usage details", { exact: true }).click();
    await expect(page.locator(".usage-model-details")).toContainText(scenario.cached === undefined ? "Unknown" : `${scenario.cached} / 50`);
    await page.reload();
    await page.getByText("Model usage details", { exact: true }).click();
    await expect(page.locator(".usage-model-details")).toContainText("fixture");
    await test.info().attach("usage-breakdown-evidence", { body: JSON.stringify({ run: replay.run, frozen_routes: replay.runtime_snapshot.model_routing, ledger, attempts, fixture: true }, null, 2), contentType: "application/json" });
  });
}

for (const mode of ["Multi-agent", "Bounded loop"]) {
  test(`${mode}: staged calls use the same frozen usage contract`, async ({ page, request }) => {
    await page.goto("/workspace");
    await expect(page.getByText("API connected", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    if (mode === "Multi-agent") {
      await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
      await page.getByRole("button", { name: "New agent", exact: true }).click();
      const dialog = page.getByRole("dialog", { name: "Create new agent" });
      await dialog.getByLabel("Name", { exact: true }).fill("Usage fixture worker");
      await dialog.getByRole("textbox", { name: "Description", exact: true }).fill("Explain model usage breakdown");
      await dialog.getByRole("textbox", { name: "System prompt", exact: true }).fill("Explain usage briefly.");
      await dialog.getByText("Routing signals", { exact: true }).click();
      await dialog.getByRole("textbox", { name: "Capabilities", exact: true }).fill("usagefixture");
      await dialog.getByRole("textbox", { name: "Example tasks", exact: true }).fill("usage-breakdown: explain usage briefly");
      await dialog.getByLabel("Memory retrieval", { exact: true }).uncheck();
      await dialog.getByLabel("Knowledge retrieval", { exact: true }).uncheck();
      await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
      await page.getByRole("button", { name: "OK", exact: true }).click();
    } else {
      await createWorkspaceChatAgent(page, await defaultWorkspaceID(page), "Loop usage fixture");
    }
    await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
    await page.getByPlaceholder("Ask AgentFlow anything...").fill("usage-breakdown: explain usage briefly");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("usagefixture");
      await page.getByRole("button", { name: "Approve & Continue" }).click();
    }
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    const href = (await page.getByRole("link", { name: "View trace" }).getAttribute("href"))!;
    const response = await request.get(`http://127.0.0.1:18080/api/runs/${href.split("/").at(-1)}/replay`);
    expect(response.ok()).toBe(true);
    const replay = await response.json();
    const ledger = replay.usage_ledger;
    const settlements = ledger.entries.filter((entry: { kind: string }) => entry.kind === "model.settlement");
    expect(settlements.length).toBeGreaterThan(1);
    expect(ledger.totals.model_calls).toBe(settlements.length);
    expect(ledger.totals.open_reservations).toBe(0);
    expect(settlements.some((entry: { breakdown?: { cached_input_tokens: number }; estimated_cost_micros: number }) => entry.breakdown?.cached_input_tokens === 30 && entry.estimated_cost_micros === 65)).toBe(true);
    await page.goto(href);
    await page.getByText("Model usage details", { exact: true }).click();
    await expect(page.locator(".usage-model-details")).toContainText("30 / 50");
    await test.info().attach("staged-usage-evidence", { body: JSON.stringify({ mode, run: replay.run, ledger, fixture: true }, null, 2), contentType: "application/json" });
  });
}
