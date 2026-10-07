import { expect, test } from "@playwright/test";

test.skip(process.env.AGENTFLOW_SANDBOX_BROWSER_TEST !== "1", "requires the controlled sandbox CLI and disposable Postgres");
const api = "http://127.0.0.1:18080";

for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
  for (const outcome of ["complete", "cancel"]) {
    test(`${mode}: committed Tool progress survives refresh then ${outcome}`, async ({ page, request }) => {
      let runId = "";
      try {
        expect((await request.post(`${api}/__fixture/tool-progress/block`)).status()).toBe(204);
        await page.goto("/workspace");
        await expect(page.getByText("API connected", { exact: true })).toBeVisible();
        await page.getByRole("button", { name: "New conversation", exact: true }).click();
        await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
        await page.getByRole("button", { name: "New agent", exact: true }).click();
        const dialog = page.getByRole("dialog", { name: "Create new agent" });
        const capability = `progress${mode.replace(/[^a-zA-Z]/g, "").toLowerCase()}${outcome}${test.info().repeatEachIndex}`;
        await dialog.getByLabel("Name", { exact: true }).fill(`Progress ${mode} ${outcome}`);
        await dialog.getByRole("textbox", { name: "Description", exact: true }).fill("Execute sandbox progress fixture");
        await dialog.getByRole("textbox", { name: "System prompt", exact: true }).fill("Use sandbox_command and report its final receipt.");
        await dialog.getByLabel("sandbox_command", { exact: true }).check();
        await dialog.getByText("Routing signals", { exact: true }).click();
        await dialog.getByRole("textbox", { name: "Capabilities", exact: true }).fill(capability);
        await dialog.getByRole("textbox", { name: "Example tasks", exact: true }).fill("sandbox-gate progress-wait");
        await dialog.getByLabel("Memory retrieval", { exact: true }).uncheck();
        await dialog.getByLabel("Knowledge retrieval", { exact: true }).uncheck();
        await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
        await page.getByRole("button", { name: "OK", exact: true }).click();
        await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
        await page.getByPlaceholder("Ask AgentFlow anything...").fill("sandbox-gate progress-wait: run a scratch command");
        await page.getByRole("button", { name: "Send message", exact: true }).click();
        if (mode === "Multi-agent") {
          await page.getByText("Routing requirements", { exact: true }).click();
          await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill(capability);
          await page.getByRole("button", { name: "Approve & Continue" }).click();
        }
        const href = (await page.getByRole("link", { name: "View trace" }).getAttribute("href"))!;
        const id = href.split("/").at(-1)!;
        runId = id;
        const read = async () => {
          const response = await request.get(`${api}/api/runs/${id}/replay`);
          expect(response.ok()).toBe(true);
          return response.json();
        };
        await expect.poll(async () => (await read()).projection.tool_progress.some((item: { phase: string }) => item.phase === "executing")).toBe(true);
        let panel = page.getByRole("region", { name: "Tool progress" });
        await panel.locator("summary").click();
        await expect(panel).toContainText("executing");
        await expect(panel).toContainText("Running");
        await page.reload();
        panel = page.getByRole("region", { name: "Tool progress" });
        await panel.locator("summary").click();
        await expect(panel).toContainText("executing");
        await expect(panel).toContainText("Saved at event");
        if (outcome === "complete") {
          expect((await request.post(`${api}/__fixture/tool-progress/release`)).status()).toBe(204);
          await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
          await expect(panel).toContainText("Completed");
        } else {
          await page.locator(".topbar").getByRole("button", { name: "Stop", exact: true }).click();
          await expect(page.getByLabel("Task status: canceled", { exact: true })).toBeVisible();
          await expect(panel).toContainText("Incomplete");
        }
        await page.goto(href);
        panel = page.getByRole("region", { name: "Tool progress" });
        await panel.locator("summary").click();
        await expect(panel).toContainText(outcome === "complete" ? "Completed" : "Incomplete");
        await page.reload();
        await panel.locator("summary").click();
        await expect(panel).toContainText(outcome === "complete" ? "Completed" : "Incomplete");
        const replay = await read();
        expect(replay.projection.invariant_failures).toEqual([]);
        expect(replay.projection.tool_progress).toHaveLength(1);
        expect(replay.projection.tool_progress[0].turn_id).toBeTruthy();
        expect(!!replay.projection.tool_progress[0].stage_id).toBe(mode !== "Single agent");
        const progress = replay.run_events.filter((item: { type: string }) => item.type === "tool.progress");
        expect(progress.length).toBeGreaterThan(0);
        expect(progress.length).toBeLessThanOrEqual(32);
        await test.info().attach("tool-progress-evidence.json", { body: JSON.stringify({
          schema: "bounded-tool-progress-evidence-v1", mode, outcome, run: replay.run,
          input: "sandbox-gate progress-wait: run a scratch command", runtime_snapshot: replay.runtime_snapshot,
          progress, projection: replay.projection.tool_progress, effects: replay.tool_effects,
          checks: ["real sandbox Binding and Executor", "live progress", "refresh", "terminal state", "Replay reload", "no invented Stage"],
          limitations: ["deterministic provider and sbx CLI fixtures", "disposable Postgres; not live microVM isolation or model quality"]
        }, null, 2), contentType: "application/json" });
      } finally {
        // Failed assertions must not leave a blocked Run consuming capacity
        // and turn later cases into misleading admission/routing failures.
        if (runId) await request.post(`${api}/api/runs/${runId}/cancel`);
        await request.post(`${api}/__fixture/tool-progress/release`);
      }
    });
  }
}
