import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

test("read-only planning freezes authority and starts a separate authorized execution", async ({ page, request }) => {
  test.skip(process.env.AGENTFLOW_PLANNING_TEST !== "1", "requires isolated planning fixtures");
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  const agent = await createWorkspaceChatAgent(page, workspace, "Project planner", {
    tools: ["file_read", "file_write", "file_patch"], skills: ["fixture-method"],
    routing_hints: { capabilities: ["planning"], task_examples: ["planning-gate"], exclusions: [] }
  });
  const records: unknown[] = [];
  let unconsumed: { run: { id: string; conversation_id: string }; plan: { hash: string } } | undefined;
  for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: new RegExp(mode) }).click();
    await page.getByRole("checkbox", { name: "Read-only planning", exact: true }).check();
    if (mode === "Single agent") await test.info().attach("planning-composer.png", { body: await page.screenshot(), contentType: "image/png" });
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(`planning-gate: read report.md and propose a sourced report, attempt no changes`);
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("planning");
      await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
    }
    await expect(page.locator(".message.assistant").last()).toContainText("Proposed report", { timeout: 45000 });
    await expect(page.getByLabel("Execution restriction: read-only planning", { exact: true })).toBeVisible();
    await page.reload();
    await expect(page.getByLabel("Execution restriction: read-only planning", { exact: true })).toBeVisible();
    const href = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!;
    const id = href.split("/").at(-1)!;
    const replay = await (await request.get(`http://127.0.0.1:18080/api/runs/${id}/replay`, { headers })).json();
    expect(replay.runtime_snapshot.execution_constraint).toBe("plan_only");
    expect(replay.runtime_snapshot.tools.some((t: { name: string }) => ["file_write", "file_patch", "update_task_state", "sandbox_command"].includes(t.name))).toBe(false);
    expect(replay.run_events.some((e: { payload: { policy_reason?: string } }) => e.payload.policy_reason === "plan_only_denied")).toBe(true);
    expect(replay.tool_effects ?? []).toHaveLength(0);
    expect(replay.plan.sources).toHaveLength(1);
    expect(replay.plan.hash).toMatch(/^sha256:/);
    await page.goto(href);
    await page.reload();
    await expect(page.getByRole("heading", { name: "Read-only plan", exact: true })).toBeVisible();
    if (mode === "Single agent") {
      await page.getByRole("heading", { name: "Read-only plan", exact: true }).scrollIntoViewIfNeeded();
      await page.getByText("Proposal", { exact: true }).click();
      await test.info().attach("planning-desktop.png", { body: await page.screenshot(), contentType: "image/png" });
      await page.getByRole("button", { name: "Start execution", exact: true }).click();
      await expect(page).toHaveURL(/workspace\?conversation=/);
      await expect(page.locator(".message.assistant").last()).toContainText("Report executed", { timeout: 45000 });
      const executionID = (await page.getByRole("link", { name: "View trace", exact: true }).getAttribute("href"))!.split("/").at(-1)!;
      expect(executionID).not.toBe(id);
      const execution = await (await request.get(`http://127.0.0.1:18080/api/runs/${executionID}/replay`, { headers })).json();
      expect(execution.runtime_snapshot.plan_origin.run_id).toBe(id);
      expect(execution.runtime_snapshot.execution_constraint ?? "").not.toBe("plan_only");
      expect(execution.tool_effects.some((e: { status: string }) => e.status === "committed")).toBe(true);
      const duplicate = await request.post("http://127.0.0.1:18080/api/chat", { headers, data: { conversation_id: replay.run.conversation_id, agent_id: agent.id, mode: "single", message: "Execute plan", plan_origin: { run_id: id, hash: replay.plan.hash } } });
      expect(duplicate.status()).toBe(409);
      records.push({ execution });
    }
    records.push({ mode, replay });
    if (mode === "Bounded loop") unconsumed = replay;
    await page.goto("/workspace");
  }
  expect(unconsumed).toBeDefined();
  const base = { conversation_id: unconsumed!.run.conversation_id, message: "Execute reviewed plan", plan_origin: { run_id: unconsumed!.run.id, hash: unconsumed!.plan.hash } };
  const failures = [];
  const foreignWorkspaceResponse = await request.post("http://127.0.0.1:18080/api/workspaces", { data: { name: "Other planning Workspace" } });
  expect(foreignWorkspaceResponse.ok()).toBe(true);
  const foreignWorkspace = await foreignWorkspaceResponse.json();
  const foreign = await request.post("http://127.0.0.1:18080/api/chat", { headers: { "X-Workspace-ID": foreignWorkspace.id }, data: base });
  expect(foreign.status()).toBe(404);
  failures.push({ reason: "cross Workspace", status: foreign.status() });
  for (const [reason, data, status] of [
    ["hash mismatch", { ...base, plan_origin: { ...base.plan_origin, hash: "wrong" } }, 409],
    ["unknown constraint", { ...base, execution_constraint: "unsafe" }, 400],
    ["in-place upgrade", { ...base, execution_constraint: "plan_only" }, 400]
  ] as const) {
    const response = await request.post("http://127.0.0.1:18080/api/chat", { headers, data });
    expect(response.status()).toBe(status);
    failures.push({ reason, status: response.status(), body: await response.json() });
  }
  expect((await request.post("http://127.0.0.1:18080/__fixture/planning/source", { data: { content: "Changed source after plan completion" } })).ok()).toBe(true);
  const changed = await request.post("http://127.0.0.1:18080/api/chat", { headers, data: base });
  expect(changed.status()).toBe(409);
  expect((await changed.json()).error).toContain("plan source changed");
  failures.push({ reason: "material drift", status: changed.status() });
  const contracts = await (await request.get("http://127.0.0.1:18080/__fixture/contracts")).json();
  expect(contracts.failures ?? []).toEqual([]);
  // A stale backend can ignore unknown JSON fields. Capability negotiation must
  // reject before POST /chat, not present a false read-only guarantee.
  await page.route("**/health", route => route.fulfill({ json: { status: "ok" } }));
  await page.goto("/workspace");
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: /Single agent/ }).click();
  await page.getByRole("checkbox", { name: "Read-only planning", exact: true }).check();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("planning-gate: incompatible backend must receive no task");
  let chatRequests = 0;
  page.on("request", req => { if (req.method() === "POST" && new URL(req.url()).pathname === "/api/chat") chatRequests++; });
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByText(/Backend does not support read-only planning/)).toBeVisible();
  expect(chatRequests).toBe(0);
  failures.push({ reason: "incompatible backend", chat_requests: chatRequests });
  await test.info().attach("planning-evidence.json", { body: JSON.stringify({ schema: "read-only-planning-browser-v1", workspace_id: workspace, records, failures, provider_contracts: contracts, limitations: ["deterministic provider", "explicitly read text files only", "startup checks are not a filesystem transaction"] }, null, 2), contentType: "application/json" });
});
