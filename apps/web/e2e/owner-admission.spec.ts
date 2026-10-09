import { expect, test, type Page } from "@playwright/test";
import { createWorkspaceChatAgent } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";
const web = "http://127.0.0.1:13000";
test.skip(process.env.AGENTFLOW_OWNER_ADMISSION_TEST !== "1" || process.env.AGENTFLOW_IDENTITY_TEST !== "1", "requires owner cap 1/global cap 2, signed OIDC and disposable Postgres");

async function signIn(page: Page) {
  await page.goto(`${web}/workspace`);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Workspace: / })).toBeVisible();
  return (await page.request.get(`${api}/api/auth/session`)).json();
}

async function send(page: Page, message: string) {
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill(message);
  await page.getByRole("button", { name: "Send message", exact: true }).click();
}

async function runId(page: Page) {
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  expect(href).toMatch(/^\/runs\/run_/);
  return href!.split("/").at(-1)!;
}

test("two authenticated owners: shared Workspace capacity, spoof rejection, cancellation and persisted diagnostics", async ({ page, browser }, testInfo) => {
  const a = await signIn(page);
  await createWorkspaceChatAgent(page, a.personal_workspace, "Owner A chat");
  const sameOwner = await page.request.post(`${api}/api/workspaces`, { headers: { Origin: web }, data: { name: "Owner quota second space" } });
  expect(sameOwner.status()).toBe(201);
  const secondSpace = await sameOwner.json();
  await send(page, "stream-gate: keep owner A's model connection active");
  await expect(page.locator(".message.assistant").last()).toContainText("First token");
  const firstId = await runId(page);
  const second = await page.context().newPage();
  const stranger = await browser.newContext();
  try {
    await second.goto(`${web}/workspace`);
    await second.getByRole("button", { name: /^Workspace: / }).click();
    await second.getByRole("option", { name: secondSpace.name, exact: true }).click();
    await createWorkspaceChatAgent(second, secondSpace.id, "Owner A second space", { memory_enabled: true });
    await send(second, "owner queue should not be bypassed by a second Workspace");
    await expect(second.getByLabel("Task status: failed", { exact: true })).toBeVisible();
    await expect(second.locator(".composer .error")).toContainText("owner_model_queue_full");
    const failedId = await runId(second);
    await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-quota-stranger" } });
    const other = await stranger.newPage();
    const b = await signIn(other);
    expect(b.user.id).not.toBe(a.user.id);
    await createWorkspaceChatAgent(other, b.personal_workspace, "Owner B chat");
    const spoofed = await page.request.post(`${api}/api/memories/search`, {
      headers: { Origin: web, "X-Workspace-ID": secondSpace.id, "X-Owner-ID": b.user.id }, data: { query: "owner scope test" }
    });
    expect(spoofed.status()).toBe(429);
    expect(spoofed.headers()["retry-after"]).toBe("1");
    expect((await spoofed.json()).code).toBe("owner_model_queue_full");
    expect((await other.request.get(`${api}/api/runs/${firstId}`, { headers: { "X-Workspace-ID": a.personal_workspace } })).status()).toBe(404);
    await send(other, "owner B can still implement a Go API while owner A is full");
    await expect(other.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    const bId = await runId(other);
    // Stop, unlike closing a browser subscription, must release the transport.
    await page.locator(".topbar").getByRole("button", { name: "Stop", exact: true }).click();
    await expect(page.getByLabel("Task status: canceled", { exact: true })).toBeVisible();
    await send(second, "Please remember that I prefer concise explanations in all future answers.");
    await expect(second.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    const restoredId = await runId(second);
    const replay = await (await second.request.get(`${api}/api/runs/${restoredId}/replay`, { headers: { "X-Workspace-ID": secondSpace.id } })).json();
    const rejected = await (await second.request.get(`${api}/api/runs/${failedId}/replay`, { headers: { "X-Workspace-ID": secondSpace.id } })).json();
    const attempts = replay.run_events.filter((event: { type: string }) => event.type === "model.attempt_finished");
    expect(attempts.length).toBeGreaterThan(0);
    expect(attempts.every((event: { payload: { owner_capacity_wait_ms?: number } }) => typeof event.payload.owner_capacity_wait_ms === "number")).toBe(true);
    expect(rejected.run_events.some((event: { type: string; payload: { error_kind?: string } }) => event.type === "model.attempt_finished" && event.payload.error_kind === "owner_model_queue_full")).toBe(true);
    await expect.poll(async () => {
      const latest = await (await second.request.get(`${api}/api/runs/${restoredId}/replay`, { headers: { "X-Workspace-ID": secondSpace.id } })).json();
      return latest.run_events.some((event: { type: string }) => event.type === "memory.sync.completed");
    }).toBe(true);
    await second.goto(`${web}/runs/${restoredId}`);
    await second.reload();
    await expect(second.locator(".replay-status")).toHaveText("completed");
    await testInfo.attach("owner-model-admission-evidence.json", {
      body: JSON.stringify({ schema: "owner-model-admission-evidence-v1", provider: "deterministic streaming fixture", identities: [a.user.id, b.user.id], workspaces: [a.personal_workspace, secondSpace.id, b.personal_workspace], limits: { global: 2, owner: 1, owner_queue: 0, owner_wait_seconds: 3 }, runs: { active_then_canceled: firstId, owner_overflow: failedId, independent_owner: bId, capacity_restored: restoredId }, rejected, replay, checks: { separate_owners_progress: true, same_owner_workspace_shares_quota: true, header_spoof: 429, foreign_run: 404, stop_reclaims_transport: true, background_memory_commits: true, reload: true }, limitations: ["single-process", "no strict fairness guarantee", "not live provider performance", "waiting queue/timeout/retry/close covered by deterministic Go tests"] }, null, 2), contentType: "application/json"
    });
  } finally {
    await page.request.post(`${api}/__fixture/release`);
    await stranger.close();
    await second.close();
    await page.request.post(`${api}/__fixture/identity/subject`, { data: { subject: "fixture-operator" } });
  }
});

for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
  test(`${mode}: owner capacity survives a multi-round Tool loop and staged continuation`, async ({ page }, testInfo) => {
    const session = await signIn(page);
    const capability = `ownertools${mode.toLowerCase().replace(/[^a-z]/g, "")}`;
    const agent = await createWorkspaceChatAgent(page, session.personal_workspace, `Owner Tools ${mode}`, {
      tools: ["get_current_time"], routing_hints: { capabilities: [capability], task_examples: ["owner-tools: implement and test a Go backend API with time zone evidence."], exclusions: [] }
    });
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
    await page.getByPlaceholder("Ask AgentFlow anything...").fill("owner-tools: implement and test a Go backend API with time zone evidence.");
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill(capability);
      await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
    }
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    const id = await runId(page);
    const replay = await (await page.request.get(`${api}/api/runs/${id}/replay`)).json();
    expect(replay.run.workspace_id).toBe(session.personal_workspace);
    if (mode === "Single agent") expect(replay.run.agent_id).toBe(agent.id);
    const attempts = replay.run_events.filter((event: { type: string }) => event.type === "model.attempt_finished");
    expect(attempts.length).toBeGreaterThanOrEqual(5);
    expect(attempts.every((event: { payload: { owner_capacity_wait_ms?: number; status: string } }) => typeof event.payload.owner_capacity_wait_ms === "number" && event.payload.status === "completed")).toBe(true);
    expect(replay.run_events.filter((event: { type: string; payload: { tool_name?: string } }) => event.type === "tool.completed" && event.payload.tool_name === "get_current_time").length).toBeGreaterThanOrEqual(4);
    if (mode === "Single agent") {
      expect(attempts.some((event: { stage_id?: string }) => !!event.stage_id)).toBe(false);
    } else {
      expect(attempts.some((event: { stage_id?: string }) => !!event.stage_id)).toBe(true);
    }
    await testInfo.attach("owner-tool-loop-evidence.json", {
      body: JSON.stringify({ mode, owner: session.user.id, workspace: session.personal_workspace, limits: { global: 2, owner: 1, queue: 0 }, replay, fixture: true, limitations: ["deterministic Tool/model fixture, not live model performance", "per-attempt capacity, not Run scheduling fairness"] }, null, 2), contentType: "application/json"
    });
  });
}
