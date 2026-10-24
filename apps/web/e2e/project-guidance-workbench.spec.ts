import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test.beforeEach(() => test.skip(process.env.AGENTFLOW_PROJECT_GUIDANCE_TEST !== "1", "requires reviewed project fixtures"));

test("Project guidance workbench: select, submit all modes and inspect saved instructions", async ({ page, request }, info) => {
  await page.goto("/workspace");
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  const api = "http://127.0.0.1:18080";
  const agent = await createWorkspaceChatAgent(page, workspace, "Reviewed project reporter", {
    routing_hints: { capabilities: ["project-guidance"], task_examples: ["project-guidance-gate"], exclusions: [] }
  });
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  const runs = [];
  try {
    await page.getByRole("button", { name: "Project guidance", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Project guidance", exact: true });
    await dialog.getByRole("button", { name: /^Project:/ }).click();
    await dialog.getByRole("option", { name: /^project / }).click();
    await dialog.getByRole("checkbox", { name: "Select report.md", exact: true }).check();
    await info.attach("project-guidance-dialog-desktop.png", { body: await page.screenshot(), contentType: "image/png" });
    await dialog.getByRole("button", { name: "Apply project", exact: true }).click();
    const selected = page.getByRole("button", { name: "Project guidance: project, 1 file", exact: true });
    await expect(selected).toBeFocused();
    await selected.click();
    await dialog.getByRole("checkbox", { name: "Select report.md", exact: true }).uncheck();
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(selected).toBeVisible();
    for (const width of [1280, 1920]) {
      await page.setViewportSize({ width, height: 1000 });
      const frame = await page.locator(".composer-inner").boundingBox();
      const field = await page.locator(".composer-inner textarea").boundingBox();
      const trigger = await selected.boundingBox();
      expect(field!.width).toBeGreaterThan(400);
      expect(trigger!.x + trigger!.width).toBeLessThanOrEqual(field!.x);
      expect(field!.x + field!.width).toBeLessThanOrEqual(frame!.x + frame!.width);
      const agentName = page.locator(".agent-select .selection-trigger > span");
      expect(await agentName.evaluate(element => element.scrollHeight <= element.clientHeight)).toBe(true);
      if (width === 1280) await info.attach("project-guidance-composer-laptop.png", { body: await page.locator(".composer").screenshot(), contentType: "image/png" });
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.reload();
    await expect(selected).toBeVisible();

    for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
      await page.getByRole("button", { name: "New conversation", exact: true }).click();
      await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: new RegExp(mode) }).click();
      const submitted = page.waitForRequest(req => req.url().endsWith("/api/chat") && req.method() === "POST");
      await page.getByPlaceholder("Ask AgentFlow anything...").fill("project-guidance-gate: summarize authorized report material");
      await page.getByRole("button", { name: "Send message", exact: true }).click();
      expect((await submitted).postDataJSON().project_context).toEqual({ root: "project", paths: ["report.md"] });
      if (mode === "Multi-agent") {
        await page.getByText("Routing requirements", { exact: true }).click();
        await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("project-guidance");
        await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
      }
      await expect(selected).toBeEnabled();
      const runId = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
      const replay = await (await request.get(`${api}/api/runs/${runId}/replay`, { headers })).json();
      expect(replay.run.status).toBe("completed");
      const frozen = replay.runtime_snapshot.project_guidance;
      expect(frozen.selection).toEqual({ root: "project", paths: ["report.md"] });
      expect(frozen.entries[0].hash).toMatch(/^[a-f0-9]{64}$/);
      const context = replay.run_events.find((event: { type: string }) => event.type === "context.assembled");
      expect(context.payload.manifest.entries).toEqual(expect.arrayContaining([expect.objectContaining({
        source: "project_guidance", source_path: "project/GUIDE.md", content_hash: frozen.entries[0].hash, selected: true
      })]));
      runs.push({ mode, run_id: runId, snapshot: frozen, manifest: context.payload.manifest });
      await page.getByRole("link", { name: "View trace", exact: true }).click();
      await page.reload();
      await page.getByText("Saved project guidance", { exact: true }).click();
      await expect(page.locator(".frozen-project-guidance")).toContainText(frozen.entries[0].hash);
      await page.getByText("Guidance text", { exact: true }).first().click();
      await expect(page.locator(".frozen-project-guidance pre").first()).toContainText("BROWSER_PROJECT_GUIDANCE");
      if (mode === "Single agent") await info.attach("project-guidance-replay-desktop.png", { body: await page.screenshot(), contentType: "image/png" });
      await page.getByRole("link", { name: "Back to chat", exact: true }).click();
    }
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await selected.click();
    await dialog.getByRole("button", { name: "Clear project", exact: true }).click();
    await expect(page.getByRole("button", { name: "Project guidance", exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: "Project guidance", exact: true })).toBeVisible();
    await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: "Direct Single agent", exact: true }).click();
    const cleared = page.waitForRequest(req => req.url().endsWith("/api/chat") && req.method() === "POST");
    await page.getByPlaceholder("Ask AgentFlow anything...").fill("Summarize without project conventions");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    expect((await cleared).postDataJSON().project_context).toBeUndefined();
    await expect(page.getByRole("button", { name: "Project guidance", exact: true })).toBeEnabled();
    const contracts = await (await request.get(`${api}/__fixture/contracts`)).json();
    expect(contracts.failures ?? []).toEqual([]);
    expect(errors).toEqual([]);
    await info.attach("project-guidance-workbench-evidence.json", { contentType: "application/json", body: JSON.stringify({
      workspace_id: workspace, agent_id: agent.id, runs,
      checks: ["real form selection", "all mode request bodies", "strict provider delivery", "cancel preserves draft", "refresh retains selection", "clear persists", "Replay provenance and text"],
      limitations: ["synthetic model", "not live adherence or quality"]
    }, null, 2) });
  } finally {
    expect((await request.delete(`${api}/api/agents/${agent.id}`, { headers })).ok()).toBe(true);
  }
});

test("Project guidance errors stay visible and do not silently drop the selection", async ({ page }) => {
  await page.goto("/workspace");
  const workspace = await defaultWorkspaceID(page);
  const agent = await createWorkspaceChatAgent(page, workspace, "Guidance error fixture");
  try {
    await page.route("**/api/project-guidance/projects", route => route.fulfill({ status: 503, json: { error: "Guidance configuration unavailable" } }));
    await page.getByRole("button", { name: "Project guidance", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Project guidance", exact: true });
    await expect(dialog.getByRole("alert")).toContainText("Guidance configuration unavailable");
    await expect(dialog.getByRole("button", { name: "Apply project", exact: true })).toBeDisabled();
    await page.unroute("**/api/project-guidance/projects");
    await dialog.getByRole("button", { name: "Retry", exact: true }).click();
    await dialog.getByRole("button", { name: /^Project:/ }).click();
    await dialog.getByRole("option", { name: /^project / }).click();
    await dialog.getByRole("checkbox", { name: "Select report.md", exact: true }).check();
    await dialog.getByRole("button", { name: "Apply project", exact: true }).click();
    await page.route("**/api/project-guidance/projects", route => route.fulfill({ status: 404, json: { error: "route not found" } }));
    let chats = 0;
    page.on("request", req => { if (req.url().endsWith("/api/chat") && req.method() === "POST") chats++; });
    await page.getByPlaceholder("Ask AgentFlow anything...").fill("Do not lose my selected project");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    await expect(page.locator(".composer .error")).toContainText("Project guidance");
    await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("Do not lose my selected project");
    expect(chats).toBe(0);
  } finally {
    await page.request.delete(`http://127.0.0.1:18080/api/agents/${agent.id}`, { headers: { "X-Workspace-ID": workspace } });
  }
});
