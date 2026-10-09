import { test, expect, type Page, type APIRequestContext } from "@playwright/test";

const api = "http://127.0.0.1:18080";
let priorFailures = 0;
let browserErrors: string[] = [];

test.beforeEach(async ({ page, request }) => {
  priorFailures = (await read(request, "/__fixture/contracts")).failures?.length ?? 0;
  browserErrors = [];
  page.on("pageerror", error => browserErrors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await expect(page.getByRole("heading", { name: "New conversation", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
});

test.afterEach(async ({ request }, info) => {
  const contracts = await read(request, "/__fixture/contracts");
  await info.attach("provider-contracts", { body: JSON.stringify(contracts, null, 2), contentType: "application/json" });
  expect((contracts.failures ?? []).slice(priorFailures)).toEqual([]);
  expect(browserErrors).toEqual([]);
});

async function read(request: APIRequestContext, path: string) {
  const response = await request.get(`${api}${path}`);
  expect(response.ok(), `${path}: ${response.status()}`).toBe(true);
  return response.json();
}

async function configureAgent(page: Page, name: string) {
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Create new agent" });
  await dialog.getByLabel("Name", { exact: true }).fill(name);
  await dialog.getByRole("textbox", { name: "Description", exact: true }).fill("state-protocol structured evidence specialist");
  await dialog.getByRole("textbox", { name: "System prompt", exact: true }).fill("Use fixture-method and update_task_state to preserve evidence.");
  await dialog.getByText("Routing signals", { exact: true }).click();
  await dialog.getByRole("textbox", { name: "Capabilities", exact: true }).fill(name.toLowerCase().replace(/[^a-z]/g, ""));
  await dialog.getByLabel("Memory retrieval", { exact: true }).uncheck();
  await dialog.getByLabel("Knowledge retrieval", { exact: true }).uncheck();
  await dialog.getByLabel("fixture-method", { exact: true }).check();
  await dialog.getByRole("checkbox", { name: "calculator", exact: true }).check();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await page.getByRole("button", { name: "OK", exact: true }).click();
  await expect(page.getByRole("button", { name: `Agent: ${name}`, exact: true })).toBeVisible();
}

async function submit(page: Page, prompt: string) {
  await page.getByPlaceholder("Ask AgentFlow anything...").fill(prompt);
  await page.getByRole("button", { name: "Send message", exact: true }).click();
}

async function identity(page: Page) {
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  expect(href).toMatch(/^\/runs\/run_/);
  return href!.split("/").at(-1)!;
}

async function retainRun(request: APIRequestContext, runId: string, prompt: string) {
  const replay = await read(request, `/api/runs/${runId}/replay`);
  const effects = await read(request, `/api/runs/${runId}/tool-effects`);
  await test.info().attach("runtime-evidence", {
    body: JSON.stringify({
      scenario: test.info().title, prompt, provider: "deterministic-strict-fixture",
      persistence: "disposable-postgres", run: replay.run,
      runtime_snapshot: replay.runtime_snapshot, projection: replay.projection,
      task_state_revisions: replay.task_state_revisions, effects: effects.effects,
      event_types: replay.run_events.map((event: { type: string }) => event.type),
      limitations: ["not live model quality", "not real network Tool egress", "not crash recovery"]
    }, null, 2), contentType: "application/json"
  });
  return { replay, effects: effects.effects };
}

for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
  test(`${mode}: Skill + reasoning continuation + ${mode === "Multi-agent" ? "isolated read-only Worker" : "guarded Task State write"}`, async ({ page, request }) => {
    await configureAgent(page, `Functional ${mode}`);
    const skills = await read(request, "/api/skills");
    expect(skills.some((skill: { name: string }) => skill.name === "fixture-method")).toBe(true);
    expect(JSON.stringify(skills)).not.toContain("FIXTURE_SKILL_BODY");
    await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
    const prompt = "state-protocol: preserve structured evidence using fixture-method";
    await submit(page, prompt);
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("functionalmultiagent");
      await page.getByRole("button", { name: "Approve & Continue" }).click();
    }
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    await expect(page.locator(".message.assistant").last()).toContainText("Evidence saved.");
    await expect(page.locator(".message.assistant").last()).not.toContainText("Provisional tool commentary");
    expect(await page.locator("body").innerText()).not.toContain("FIXTURE_PRIVATE_REASONING");
    const runId = await identity(page);
    const { replay, effects } = await retainRun(request, runId, prompt);
    expect(replay.run.status).toBe("completed");
    expect(replay.runtime_snapshot.mode).toBe(mode === "Single agent" ? "single" : mode === "Multi-agent" ? "multi_agent" : "autonomous");
    expect(replay.runtime_snapshot.model_routing.routes.some((route: { model: string }) => route.model === "fixture-model")).toBe(true);
    expect(replay.runtime_snapshot.embedding.model).toBe("fixture-embedding");
    expect(replay.runtime_snapshot.skills.some((skill: { name: string; instructions: string }) => skill.name === "fixture-method" && skill.instructions.includes("FIXTURE_SKILL_BODY"))).toBe(true);
    expect(JSON.stringify(replay)).not.toContain("fixture-not-a-secret");
    expect(replay.projection.invariant_failures ?? []).toEqual([]);
    const conversationId = replay.run.conversation_id;
    const state = await read(request, `/api/conversations/${conversationId}/task-state`);
    if (mode === "Multi-agent") {
      expect(replay.task_state_revisions).toHaveLength(0);
      expect(state.version).toBe(0);
      expect(effects).toHaveLength(0);
      const toolEvents = replay.run_events.filter((event: { type: string }) => event.type === "tool.completed");
      expect(toolEvents).toHaveLength(2);
      for (const event of toolEvents) expect(event.stage_id).toBeTruthy();
    } else {
      expect(replay.task_state_revisions).toHaveLength(1);
      expect(effects.filter((effect: { status: string }) => effect.status === "committed")).toHaveLength(1);
      const receipt = effects.find((effect: { status: string }) => effect.status === "committed");
      expect(receipt.turn_id).toBeTruthy();
      if (mode === "Single agent") expect(receipt.stage_id ?? "").toBe("");
      else expect(receipt.stage_id).toBeTruthy();
      expect(state.version).toBe(1);
      expect(state.goal).toBe("Preserve evidence");
      expect(state.tasks[0].details).toBe("Exact durable fact");
      const stale = await request.patch(`${api}/api/conversations/${conversationId}/task-state`, {
        data: { expected_version: 0, operations: [{ type: "set_goal", goal: "stale overwrite" }] }
      });
      expect(stale.status()).toBe(409);
      expect((await read(request, `/api/conversations/${conversationId}/task-state`)).goal).toBe("Preserve evidence");
      expect(await read(request, `/api/conversations/${conversationId}/task-state/revisions`)).toHaveLength(1);
    }
    await page.reload();
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    if (mode === "Multi-agent") {
      await expect(page.locator(".dag-node-status")).toHaveText(["completed", "completed", "completed", "completed", "completed"]);
    }
    await page.getByRole("button", { name: /^Task state/ }).click();
    await expect(page.getByRole("complementary", { name: "Conversation task state" })).toContainText(mode === "Multi-agent" ? "No structured task facts" : "Exact durable fact");
    await page.getByRole("link", { name: "View trace" }).click();
    await expect(page.getByRole("heading", { name: "Run replay", exact: true })).toBeVisible();
    await expect(page.locator(".replay-status")).toHaveText("completed");
    const skillsPanel = page.locator(".skill-evidence");
    await expect(skillsPanel).toBeVisible();
    await expect(skillsPanel).not.toHaveAttribute("open", "");
    await skillsPanel.getByText(/^Skill evidence/).click();
    await expect(skillsPanel).toContainText("Instructions included");
    await expect(skillsPanel).toContainText("Model activation");
    await skillsPanel.getByRole("button", { name: /^Inspect input/ }).first().click();
    await expect(page.locator("#run-event-detail")).toContainText("context.assembled");
    await page.reload();
    await expect(page.locator(".skill-evidence")).toBeVisible();
    expect(await page.locator("body").innerText()).not.toContain("FIXTURE_PRIVATE_REASONING");
  });
}

test("Tool-enabled answer is visible before provider completion and survives browser disconnect", async ({ page, request }) => {
  const prompt = "stream-gate: show the answer as soon as it arrives";
  await submit(page, prompt);
  await expect(page.locator(".message.assistant").last()).toHaveText(/First token/);
  await expect(page.getByLabel("Task status: running", { exact: true })).toBeVisible();
  await expect(page.locator(".message.assistant").last()).not.toContainText("finished");
  const runId = await identity(page);
  // Navigating away aborts the browser subscription, not the accepted Run.
  await page.goto("about:blank");
  const released = await request.post(`${api}/__fixture/release`);
  expect(released.status()).toBe(204);
  await expect.poll(async () => (await read(request, `/api/runs/${runId}`)).status).toBe("completed");
  const { replay } = await retainRun(request, runId, prompt);
  await page.goto(`/workspace?conversation=${replay.run.conversation_id}`);
  await expect(page.locator(".message.assistant").last()).toContainText("First token then finished.");
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
});

test("Provider failure is visible, persisted, and never mistaken for completion", async ({ page, request }) => {
  const prompt = "provider-failure: reject this request";
  await submit(page, prompt);
  await expect(page.getByLabel("Task status: failed", { exact: true })).toBeVisible();
  await expect(page.locator(".composer .error")).toContainText("invalid_request");
  await expect(page.getByText("Working...", { exact: true })).toHaveCount(0);
  const runId = await identity(page);
  const { replay } = await retainRun(request, runId, prompt);
  expect(replay.run.status).toBe("failed");
  expect(replay.run.error).toContain("invalid_request");
  await page.getByRole("link", { name: "View trace" }).click();
  await expect(page.locator(".replay-status")).toHaveText("failed");
  await expect(page.locator("body")).toContainText("Fixture rejected");
});

test("Verification UI sends a real contract and a failed verifier blocks completion", async ({ page, request }) => {
  await page.getByRole("button", { name: /^Verification/ }).click();
  const dialog = page.getByRole("dialog", { name: "Verification" });
  await dialog.getByLabel("Disabled", { exact: true }).check();
  await dialog.getByRole("combobox", { name: "Maximum attempts", exact: true }).selectOption("1");
  await dialog.getByRole("combobox", { name: "When attempts are exhausted", exact: true }).selectOption("fail");
  await dialog.getByLabel("Min characters", { exact: true }).fill("1000");
  await dialog.getByRole("button", { name: "Save policy" }).click();
  const sent = page.waitForRequest(request => request.url() === `${api}/api/chat` && request.method() === "POST");
  const prompt = "verification-gate: return a short answer";
  await submit(page, prompt);
  const body = (await sent).postDataJSON();
  expect(body.completion_contract.verifiers[0].type).toBe("text_constraints");
  await expect(page.getByLabel("Task status: failed", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Verification status: failed", { exact: true })).toBeVisible();
  const runId = await identity(page);
  const { replay } = await retainRun(request, runId, prompt);
  expect(replay.run.verification_status).toBe("failed");
  await page.getByRole("link", { name: "View trace" }).click();
  await expect(page.locator("#run-verification-evidence")).toContainText("failed");
});
