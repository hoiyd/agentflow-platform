import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test("exact-call approval survives reload and executes once across runtime modes", async ({ page, request }) => {
  test.skip(process.env.AGENTFLOW_TOOL_APPROVAL_TEST !== "1", "requires operator ask policy and isolated output roots");
  test.setTimeout(120_000);
  await page.goto("/workspace");
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  const agent = await createWorkspaceChatAgent(page, workspace, "Report editor", {
    tools: ["file_write", "file_patch"],
    routing_hints: { capabilities: ["report"], task_examples: ["tool-approval-gate: revise the managed report"], exclusions: [] }
  });
  const evidence = [];
  try {
    for (const mode of ["Single agent", "Multi-agent", "Bounded loop"] as const) {
      await page.getByRole("button", { name: "New conversation", exact: true }).click();
      await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: new RegExp(mode) }).click();
      const task = `tool-approval-gate-${mode.split(" ")[0].toLowerCase()}: revise the managed report`;
      await page.getByPlaceholder("Ask AgentFlow anything...").fill(task);
      await page.getByRole("button", { name: "Send message", exact: true }).click();
      if (mode === "Multi-agent") await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
      const panel = page.getByRole("region", { name: "Tool approvals", exact: true });
      await expect(panel.getByText("Approval required", { exact: true })).toBeVisible();
      const run = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
      const before = await (await request.get(`http://127.0.0.1:18080/api/runs/${run}/replay`, { headers })).json();
      expect(before.tool_effects.filter((item: { tool_name: string }) => item.tool_name === "file_patch")).toHaveLength(0);
      await page.reload();
      await expect(panel.getByText("Approval required", { exact: true })).toBeVisible();
      await expect(panel.getByText("Original", { exact: true })).toBeVisible();
      await expect(panel.getByText("Approved", { exact: true })).toBeVisible();
      if (mode === "Single agent") await test.info().attach("tool-approval-pending-desktop.png", { body: await page.screenshot(), contentType: "image/png" });
      await panel.getByRole("button", { name: "Approve once & continue", exact: true }).click();
      await expect(page.locator(".message.assistant").last()).toContainText("Report edit verified.");
      const approvals = await (await request.get(`http://127.0.0.1:18080/api/runs/${run}/tool-approvals`, { headers })).json();
      expect(approvals).toHaveLength(1);
      expect(approvals[0].status).toBe("consumed");
      const duplicate = await request.post(`http://127.0.0.1:18080/api/runs/${run}/tool-approvals/${approvals[0].id}/resume`, { headers });
      expect(duplicate.status()).toBe(409);
      const after = await (await request.get(`http://127.0.0.1:18080/api/runs/${run}/replay`, { headers })).json();
      expect(after.run.status).toBe("completed");
      expect(after.runtime_snapshot).toEqual(before.runtime_snapshot);
      const patches = after.tool_effects.filter((item: { tool_name: string }) => item.tool_name === "file_patch");
      expect(patches).toHaveLength(1);
      expect(patches[0].status).toBe("committed");
      expect(after.projection.invariant_failures).toEqual([]);
      await page.getByRole("link", { name: "View trace", exact: true }).click();
      await page.reload();
      await expect(page.getByRole("region", { name: "Tool approvals", exact: true }).getByText("Execution claim consumed", { exact: true })).toBeVisible();
      evidence.push({ mode, task, run_id: run, approval: approvals[0], snapshot: after.runtime_snapshot, patches });
      await page.goto("/workspace");
    }
    await test.info().attach("tool-approval-evidence.json", {
      body: JSON.stringify({ schema: "tool-approval-browser-evidence-v1", workspace_id: workspace, cases: evidence,
        checks: ["real UI/HTTP/Runtime/Binding/Postgres", "no patch before approval", "reload", "exact approved patch", "single receipt", "duplicate resume rejected", "frozen Snapshot", "Replay evidence"],
        limitations: ["deterministic provider", "managed-output file_patch only", "not external exactly-once"] }, null, 2),
      contentType: "application/json"
    });
  } finally {
    await request.delete(`http://127.0.0.1:18080/api/agents/${agent.id}`, { headers });
  }
});
