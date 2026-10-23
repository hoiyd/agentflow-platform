import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test("Web fetch: owned configuration, scoped rejection, all modes and Replay reload", async ({ page, request }, info) => {
  test.setTimeout(120_000);
  const browserErrors: string[] = [];
  page.on("pageerror", error => browserErrors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  const api = "http://127.0.0.1:18080";
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  const card = page.locator(".tool-card").filter({ has: page.getByRole("heading", { name: "web_fetch", exact: true }) });
  await expect(card.getByRole("checkbox")).toBeChecked();
  await expect(card.getByRole("checkbox")).toBeEnabled();
  const unrelatedAgent = await createWorkspaceChatAgent(page, workspace, "Unrelated Agent without web access");
  const agent = await createWorkspaceChatAgent(page, workspace, "Web source boundary verifier", {
    tools: ["web_fetch"],
    routing_hints: { capabilities: ["web-fetch-boundary"], task_examples: ["Verify public source references"], exclusions: [] }
  });
  await page.getByRole("button", { name: "Configure", exact: true }).click();
  await expect(page.getByRole("dialog").getByRole("checkbox", { name: "web_fetch", exact: true })).toBeChecked();
  await page.getByRole("button", { name: "Cancel edit agent config", exact: true }).click();
  const runs = [];
  try {
    for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
      await page.getByRole("button", { name: "New conversation", exact: true }).click();
      await expect(page.getByRole("link", { name: "View trace", exact: true })).toHaveCount(0);
      // Replay navigation remounts Chat; select this fixture before hiding the selector in staged modes.
      await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: "Direct Single agent", exact: true }).click();
      await page.getByRole("button", { name: /^Agent: / }).click();
      await page.getByRole("option", { name: agent.name, exact: true }).click();
      await expect(page.getByRole("button", { name: `Agent: ${agent.name}`, exact: true })).toBeVisible();
      await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
      await page.getByPlaceholder("Ask AgentFlow anything...").fill("web-fetch-boundary: verify the article only if its source exists in this Run");
      const submitted = page.waitForRequest(request => request.url() === `${api}/api/chat` && request.method() === "POST");
      await page.getByRole("button", { name: "Send message", exact: true }).click();
      expect((await submitted).postDataJSON().agent_id, `${mode}: selected Agent`).toBe(agent.id);
      if (mode === "Multi-agent") {
        await page.getByText("Routing requirements", { exact: true }).click();
        await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("web-fetch-boundary");
        await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
      }
      await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
      await expect(page.locator(".message.assistant").last()).toContainText("Web fetch boundary confirmed");
      const link = page.getByRole("link", { name: "View trace", exact: true });
      const id = (await link.getAttribute("href"))!.split("/").at(-1)!;
      const response = await request.get(`${api}/api/runs/${id}/replay`, { headers });
      expect(response.ok()).toBe(true);
      const replay = await response.json();
      expect(replay.run.status).toBe("completed");
      expect(replay.run.agent_id, `${mode}: executed Agent`).toBe(agent.id);
      expect(replay.runtime_snapshot.mode).toBe(mode === "Single agent" ? "single" : mode === "Multi-agent" ? "multi_agent" : "autonomous");
      expect(replay.runtime_snapshot.tools.some((item: { name: string }) => item.name === "web_fetch")).toBe(true);
      const failures = replay.run_events.filter((event: { type: string; payload: { tool_name?: string } }) => event.type === "tool.failed" && event.payload.tool_name === "web_fetch");
      expect(failures).toHaveLength(1);
      expect(failures[0].payload.error_code).toBe("security_scope_invalid");
      expect(Boolean(failures[0].stage_id)).toBe(mode !== "Single agent");
      expect(replay.projection.invariant_failures ?? []).toEqual([]);
      runs.push({ mode, run_id: id, snapshot: replay.runtime_snapshot, failure: failures[0] });
      await link.click();
      await expect(page.getByRole("heading", { name: "Run replay", exact: true })).toBeVisible();
      await page.reload();
      await page.getByRole("button", { name: /^tool.failed/ }).last().click();
      await page.getByText("Raw event payload", { exact: true }).click();
      await expect(page.locator(".event-detail pre")).toContainText("security_scope_invalid");
      await page.getByRole("link", { name: "Back to chat", exact: true }).click();
    }
    const contracts = await (await request.get(`${api}/__fixture/contracts`)).json();
    expect(contracts.failures ?? []).toEqual([]);
    expect(browserErrors).toEqual([]);
    await info.attach("web-fetch-browser-evidence.json", {
      contentType: "application/json",
      body: JSON.stringify({ workspace_id: workspace, agent_id: agent.id, unrelated_agent_id: unrelatedAgent.id, runs, checks: ["default availability", "owned Agent binding", "selected Agent retained explicitly across Replay navigation with competing Agents", "real scoped rejection in all modes", "no fabricated Stage for Single", "Postgres Replay reload"], limitations: ["deterministic model fixture", "no public HTTP call or live model quality; successful fetch has a separate backend integration gate"] }, null, 2)
    });
  } finally {
    expect((await request.delete(`${api}/api/agents/${agent.id}`, { headers })).ok()).toBe(true);
    expect((await request.delete(`${api}/api/agents/${unrelatedAgent.id}`, { headers })).ok()).toBe(true);
  }
});
