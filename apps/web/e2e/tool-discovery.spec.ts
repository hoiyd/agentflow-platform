import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test.skip(process.env.AGENTFLOW_TOOL_DISCOVERY_TEST !== "1", "opt-in lazy Schema production composition required");
const api = "http://127.0.0.1:18080";

for (const mode of ["Single agent", "Multi-agent", "Bounded loop", "Single agent unloaded call"]) {
  test(`${mode}: bounded discovery reaches the real Binding and survives reload`, async ({ page, request }, info) => {
    const errors: string[] = [];
    page.on("pageerror", error => errors.push(error.message));
    const before = await (await request.get(`${api}/__fixture/contracts`)).json();
    await page.goto("/workspace");
    await expect(page.getByText("API connected", { exact: true })).toBeVisible();
    const workspace = await defaultWorkspaceID(page);
    const capability = `discovery${mode.toLowerCase().replace(/[^a-z]/g, "")}`;
    const agent = await createWorkspaceChatAgent(page, workspace, `Discovery ${mode}`, {
      system_prompt: "Discover and use calculator for arithmetic.", tools: ["calculator"],
      description: "tool-discovery calculator calculate arithmetic specialist",
      routing_hints: { capabilities: [capability, "calculator", "calculate"], task_examples: ["tool-discovery calculator arithmetic"] }
    });
    const selectedMode = mode === "Single agent unloaded call" ? "Single agent" : mode;
    await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(selectedMode) }).click();
    const prompt = mode === "Single agent unloaded call" ? "tool-discovery-unloaded: calculate 1 + 1" : "tool-discovery: calculate 1 + 1";
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(prompt);
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill(capability);
      await page.getByRole("button", { name: "Approve & Continue" }).click();
    }
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    await expect(page.locator(".message.assistant").last()).toContainText("Evidence saved.");
    const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
    const runID = href!.split("/").at(-1)!;
    const replayResponse = await request.get(`${api}/api/runs/${runID}/replay`);
    expect(replayResponse.ok()).toBe(true);
    const replay = await replayResponse.json();
    const states = replay.run_events.filter((event: { type: string }) => event.type === "tool.discovery.updated");
    expect(replay.runtime_snapshot.tool_schema.mode).toBe("lazy");
    expect(states.some((event: { payload: { agent_id: string; reason: string; active: Array<{ name: string }> } }) =>
      event.payload.agent_id === agent.id && event.payload.reason === "search_match" && event.payload.active.some(item => item.name === "calculator"))).toBe(true);
    const completed = replay.run_events.filter((event: { type: string; payload: { tool_name: string } }) => event.type === "tool.completed" && event.payload.tool_name === "calculator");
    expect(completed).toHaveLength(1);
    expect(replay.projection.invariant_failures ?? []).toEqual([]);
    if (mode === "Single agent unloaded call") {
      expect(replay.run_events.some((event: { type: string; payload: { policy_reason?: string } }) => event.type === "tool.failed" && event.payload.policy_reason === "tool_not_loaded")).toBe(true);
    }
    await page.reload();
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    await page.getByRole("link", { name: "View trace" }).click();
    await expect(page.getByRole("heading", { name: "Run replay", exact: true })).toBeVisible();
    await page.reload();
    await expect(page.locator(".replay-status")).toHaveText("completed");
    const contracts = await (await request.get(`${api}/__fixture/contracts`)).json();
    expect((contracts.failures ?? []).slice(before.failures?.length ?? 0)).toEqual([]);
    expect(errors).toEqual([]);
    await info.attach("tool-discovery-runtime-evidence", { body: JSON.stringify({
      prompt, mode, agent_id: agent.id, run: replay.run, runtime_snapshot: replay.runtime_snapshot,
      discovery: states, tool_results: completed, contracts,
      limitations: ["deterministic provider, not live selection quality", "reload, not server crash recovery"]
    }, null, 2), contentType: "application/json" });
  });
}
