import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test("Project guidance: typed request reaches the model and Replay retains source evidence", async ({ page, request }, info) => {
  test.skip(process.env.AGENTFLOW_PROJECT_GUIDANCE_TEST !== "1", "requires reviewed project fixtures");
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  const api = "http://127.0.0.1:18080";
  const agent = await createWorkspaceChatAgent(page, workspace, "Reviewed project reporter");
  try {
    const submitted = await request.post(`${api}/api/chat`, { headers, data: {
      agent_id: agent.id, mode: "single", message: "project-guidance-gate: summarize authorized report material",
      project_context: { root: "project", paths: ["report.md"] }
    } });
    expect(submitted.ok()).toBe(true);
    const stream = await submitted.text();
    expect(stream).not.toContain("event: error");
    const runs = await (await request.get(`${api}/api/runs`, { headers })).json();
    const run = runs.find((item: { agent_id: string }) => item.agent_id === agent.id);
    expect(run?.status).toBe("completed");
    const replay = await (await request.get(`${api}/api/runs/${run.id}/replay`, { headers })).json();
    const frozen = replay.runtime_snapshot.project_guidance;
    expect(frozen.selection).toEqual({ root: "project", paths: ["report.md"] });
    expect(frozen.entries[0].hash).toMatch(/^[a-f0-9]{64}$/);
    const context = replay.run_events.find((event: { type: string }) => event.type === "context.assembled");
    expect(context.payload.manifest.entries).toEqual(expect.arrayContaining([expect.objectContaining({
      source: "project_guidance", source_path: "project/GUIDE.md", content_hash: frozen.entries[0].hash,
      selected: true, reason: "project_entry"
    })]));
    await page.goto(`/runs/${run.id}`);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Run replay", exact: true })).toBeVisible();
    await page.getByRole("button", { name: /^context.assembled/ }).first().click();
    await page.getByText("Raw event payload", { exact: true }).click();
    await expect(page.locator(".event-detail pre")).toContainText("project_guidance");
    await expect(page.locator(".event-detail pre")).toContainText(frozen.entries[0].hash);
    const contracts = await (await request.get(`${api}/__fixture/contracts`)).json();
    expect(contracts.failures ?? []).toEqual([]);
    expect(errors).toEqual([]);
    await info.attach("project-guidance-browser-evidence.json", { contentType: "application/json", body: JSON.stringify({
      workspace_id: workspace, agent_id: agent.id, run_id: run.id, snapshot: frozen, manifest: context.payload.manifest,
      checks: ["real API selection", "strict provider delivery", "frozen hash", "Replay reload and raw provenance"],
      limitations: ["synthetic model", "project selection is API/CLI-only; no new workbench form", "not live adherence or quality"]
    }, null, 2) });
  } finally {
    expect((await request.delete(`${api}/api/agents/${agent.id}`, { headers })).ok()).toBe(true);
  }
});
