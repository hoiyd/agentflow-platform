import { test, expect, type Page, type APIRequestContext } from "@playwright/test";

const api = "http://127.0.0.1:18080";
const secret = "sk-fixtureDisplayCredential123456";
test.skip(process.env.AGENTFLOW_REASONING_TEST !== "1", "explicit opt-in reasoning fixture route required");

async function read(request: APIRequestContext, path: string) {
  const response = await request.get(`${api}${path}`);
  expect(response.ok(), `${path}: ${response.status()}`).toBe(true);
  return response.json();
}

async function submit(page: Page, prompt: string) {
  await page.getByPlaceholder("Ask AgentFlow anything...").fill(prompt);
  await page.getByRole("button", { name: "Send message", exact: true }).click();
}

async function createReasoningAgent(page: Page, mode: string) {
  const capability = `reasoning${mode.toLowerCase().replace(/[^a-z]/g, "")}${test.info().repeatEachIndex}`;
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Create new agent" });
  await dialog.getByLabel("Name", { exact: true }).fill(`Reasoning ${mode}`);
  await dialog.getByRole("textbox", { name: "Description", exact: true }).fill("reasoning-display calculator specialist");
  await dialog.getByRole("textbox", { name: "System prompt", exact: true }).fill("Calculate and explain the result.");
  await dialog.getByText("Routing signals", { exact: true }).click();
  await dialog.getByRole("textbox", { name: "Capabilities", exact: true }).fill(capability);
  await dialog.getByRole("textbox", { name: "Example tasks", exact: true }).fill("reasoning-display calculate two Tool rounds");
  await dialog.getByLabel("Memory retrieval", { exact: true }).uncheck();
  await dialog.getByLabel("Knowledge retrieval", { exact: true }).uncheck();
  await dialog.getByRole("checkbox", { name: "calculator", exact: true }).check();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await page.getByRole("button", { name: "OK", exact: true }).click();
  return capability;
}

async function evidence(page: Page, request: APIRequestContext, prompt: string) {
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  const runId = href!.split("/").at(-1)!;
  const replay = await read(request, `/api/runs/${runId}/replay`);
  const messages = await read(request, `/api/conversations/${replay.run.conversation_id}/messages`);
  const requests = await read(request, `/api/runs/${runId}/model_requests`);
  const durable = JSON.stringify({ replay, messages, requests });
  expect(durable).not.toContain(secret);
  const reasoningEvents = replay.run_events.filter((item: { type: string }) => item.type === "model.reasoning");
  expect(replay.run_events.some((item: { type: string }) => item.type === "model.reasoning_delta")).toBe(false);
  const complete = reasoningEvents.filter((item: { payload: { status: string } }) => item.payload.status === "complete");
  if (complete.length) expect(durable).toContain("DISPLAY_ONLY_REASONING");
  expect(JSON.stringify(requests)).not.toContain("DISPLAY_ONLY_REASONING");
  const savedReasoning = messages.flatMap((message: { reasoning?: Array<{ run_id: string }> }) => message.reasoning ?? []).filter((entry: { run_id: string }) => entry.run_id === runId);
  if (replay.run.status === "completed") expect(savedReasoning).toHaveLength(complete.length);
  await test.info().attach("reasoning-runtime-evidence", { body: JSON.stringify({
    prompt, run: replay.run, frozen_routes: replay.runtime_snapshot.model_routing,
    event_types: replay.run_events.map((item: { type: string }) => item.type),
    model_requests: requests, provider_contracts: await read(request, "/__fixture/contracts"),
    reasoning_bindings: messages.map((message: { id: string; reasoning?: Array<{ run_id: string; model_call_id: string; text?: string }> }) => ({
      message_id: message.id, calls: (message.reasoning ?? []).map((entry) => ({
        run_id: entry.run_id, model_call_id: entry.model_call_id,
        text_bytes: new TextEncoder().encode(entry.text ?? "").length
      }))
    })),
    limits: { display_bytes_per_call: 16384, retained_browser_calls: 32 },
    privacy: { sanitized_reasoning_persisted: true, metadata_capture_content_absent: true, secret_absent: true },
    limitations: ["strict local fixture, not live provider evidence", "same-Turn continuation only", "raw live deltas are ephemeral; sanitized partial checkpoints are durable"]
  }, null, 2), contentType: "application/json" });
  return replay;
}

test.beforeEach(async ({ page }) => {
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
});

test.afterEach(async ({ request }) => {
  expect((await read(request, "/__fixture/contracts")).failures ?? []).toEqual([]);
});

for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
  test(`${mode}: reasoning survives navigation and reload across Tool rounds and answer reset`, async ({ page, request }) => {
    const capability = await createReasoningAgent(page, mode);
    await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
    const prompt = "reasoning-display: calculate with two Tool rounds";
    await submit(page, prompt);
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill(capability);
      await page.getByRole("button", { name: "Approve & Continue" }).click();
    }
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    const disclosure = page.locator(".provider-reasoning");
    await expect(disclosure).toBeVisible();
    await expect(disclosure).not.toHaveAttribute("open", "");
    await disclosure.locator("summary").click();
    await expect(disclosure.locator("li")).toHaveCount(3);
    await expect(disclosure).toContainText("[REDACTED]");
    expect(await disclosure.innerText()).not.toContain(secret);
    await expect(page.locator(".message.assistant").last()).toContainText("Evidence saved.");
    await expect(page.locator(".message.assistant").last()).not.toContainText("DISPLAY_ONLY_REASONING");
    const replay = await evidence(page, request, prompt);
    expect(replay.run.status).toBe("completed");
    await page.getByRole("button", { name: "Tools", exact: true }).click();
    await expect(disclosure).toHaveCount(0);
    await page.getByRole("button", { name: "Chat", exact: true }).click();
    await expect(disclosure).toBeVisible();
    await page.getByRole("button", { name: "Tools", exact: true }).click();
    await page.locator(".conversation-item.active").click();
    await expect(disclosure).toBeVisible();
    await disclosure.locator("summary").click();
    await expect(disclosure.locator("li")).toHaveCount(3);
    await expect(disclosure).toContainText("[REDACTED]");
    expect(await disclosure.innerText()).not.toContain(secret);
    // Re-selecting the active conversation is a view change, not a new session.
    await page.locator(".conversation-item.active").press("Enter");
    await expect(disclosure.locator("li")).toHaveCount(3);
    await evidence(page, request, prompt);
    await page.reload();
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    await expect(disclosure).toBeVisible();
    await disclosure.locator("summary").click();
    await expect(disclosure.locator("li")).toHaveCount(3);
    await expect(disclosure).toContainText("[REDACTED]");
    const title = `Reasoning ${mode} ${replay.run.id}`;
    await page.getByRole("button", { name: "Rename conversation", exact: true }).click();
    await page.getByRole("textbox", { name: "Conversation title", exact: true }).fill(title);
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await expect(disclosure).toHaveCount(0);
    await page.locator(".conversation-item").filter({ has: page.getByText(title, { exact: true }) }).click();
    await expect(disclosure).toBeVisible();
    await disclosure.locator("summary").click();
    await expect(disclosure.locator("li")).toHaveCount(3);
    await evidence(page, request, prompt);
    if (mode === "Single agent") {
      await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
      await submit(page, prompt);
      await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
      await expect(disclosure).toHaveCount(2);
      await expect(disclosure.locator("li")).toHaveCount(6);
      const next = await evidence(page, request, prompt);
      expect(next.run.id).not.toBe(replay.run.id);
      await page.reload();
      await expect(disclosure).toHaveCount(2);
      await expect(disclosure.locator("li")).toHaveCount(6);
      await evidence(page, request, prompt);
    }
  });
}

test("safe reasoning is visible before completion and browser disconnect does not cancel execution", async ({ page, request }) => {
  const prompt = "reasoning-gate: wait before final answer";
  await submit(page, prompt);
  const disclosure = page.locator(".provider-reasoning");
  await expect(disclosure).toBeVisible();
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Receiving provider reasoning");
  await expect(disclosure.locator("pre")).toContainText("DISPLAY_ONLY_REASONING");
  const prefix = await disclosure.locator("pre").innerText();
  expect(prefix).not.toContain("sk-fixture");
  await expect(disclosure).not.toContainText("Received");
  await expect(page.getByText("Working...", { exact: true })).toBeVisible();
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  const runId = href!.split("/").at(-1)!;
  const pending = await read(request, `/api/runs/${runId}/replay`);
  expect(pending.run.status).toBe("running");
  expect(pending.run_events.some((event: { type: string }) => event.type === "model.reasoning_delta")).toBe(false);
  expect(pending.run_events.filter((event: { type: string }) => event.type === "model.reasoning")
    .every((event: { payload: { text?: string } }) => !event.payload.text)).toBe(true);
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  await page.locator(".conversation-item.active").click();
  await expect(disclosure).toBeVisible();
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Receiving provider reasoning");
  await expect(disclosure.locator("pre")).toHaveText(prefix);
  await expect(page.getByRole("link", { name: "View trace" })).toHaveAttribute("href", href!);
  await expect(page.getByRole("button", { name: "Send message", exact: true })).toBeDisabled();
  await page.goto("about:blank");
  expect((await request.post(`${api}/__fixture/release`)).status()).toBe(204);
  await expect.poll(async () => (await read(request, `/api/runs/${runId}`)).status).toBe("completed");
  const run = await read(request, `/api/runs/${runId}`);
  await page.goto(`/workspace?conversation=${run.conversation_id}`);
  await expect(page.locator(".message.assistant").last()).toContainText("Evidence saved.");
  await expect(disclosure).toBeVisible();
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("DISPLAY_ONLY_REASONING");
  expect((await disclosure.locator("pre").innerText()).length).toBeGreaterThan(prefix.length);
  await test.info().attach("live-reasoning-evidence", { body: JSON.stringify({ run_id: runId,
    prefix_visible_while_running: true, prefix_bytes: new TextEncoder().encode(prefix).length,
    incomplete_credential_withheld: true, live_batches_persisted: false,
    view_change_retained_prefix: true, disconnect_did_not_cancel: true,
    completed_copy_reloaded: true
  }, null, 2), contentType: "application/json" });
  await evidence(page, request, prompt);
});

test("provider disconnect keeps failure and recovers only a sanitized incomplete prefix", async ({ page, request }) => {
  const prompt = "reasoning-disconnect: interrupt after reasoning";
  await submit(page, prompt);
  await expect(page.getByLabel("Task status: failed", { exact: true })).toBeVisible();
  await expect(page.getByRole("region", { name: "Recovered output" })).toContainText("Incomplete");
  await expect(page.getByRole("region", { name: "Recovered output" })).toContainText("DISPLAY_ONLY_REASONING");
  expect(await page.getByRole("region", { name: "Recovered output" }).innerText()).not.toContain(secret);
  await expect(page.getByText("Working...", { exact: true })).toHaveCount(0);
  await expect(page.locator(".error").filter({ hasText: "Internal Server Error" })).toBeVisible();
  expect((await evidence(page, request, prompt)).run.status).toBe("failed");
  await page.reload();
  await expect(page.getByRole("region", { name: "Recovered output" })).toContainText("Incomplete");
});

for (const mode of ["Single agent", "Multi-agent", "Bounded loop"]) {
test(`${mode}: explicit cancellation ends normally and preserves only sanitized incomplete reasoning`, async ({ page, request }) => {
  const capability = await createReasoningAgent(page, `${mode} cancellation`);
  await page.getByRole("region", { name: "Chat mode", exact: true }).getByRole("button", { name: new RegExp(mode) }).click();
  const stream = page.waitForResponse((response) => response.request().method() === "POST" &&
    (mode === "Multi-agent" ? response.url().endsWith("/continue") : response.url().endsWith("/api/chat")));
  const prompt = "reasoning-cancel: wait for cancellation";
  await submit(page, prompt);
  if (mode === "Multi-agent") {
    await page.getByText("Routing requirements", { exact: true }).click();
    await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill(capability);
    await page.getByRole("button", { name: "Approve & Continue" }).click();
  }
  const disclosure = page.locator(".provider-reasoning");
  await expect(disclosure).toBeVisible();
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Receiving provider reasoning");
  await expect(disclosure.locator("pre")).toContainText("DISPLAY_ONLY_REASONING");
  expect(await disclosure.locator("pre").innerText()).not.toContain("sk-fixture");
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  const runId = href!.split("/").at(-1)!;
  // Synchronize on durable state, not on how quickly the UI can click Stop.
  await expect.poll(async () => {
    const projection = await read(request, `/api/runs/${runId}/projection`);
    return projection.partial_outputs.some((item: { channel: string; text: string }) =>
      item.channel === "reasoning" && item.text.includes("DISPLAY_ONLY_REASONING"));
  }).toBe(true);
  await page.locator(".topbar").getByRole("button", { name: "Stop", exact: true }).click();
  await expect(page.getByLabel("Task status: canceled", { exact: true })).toBeVisible();
  const body = await (await stream).text();
  expect(body).toContain('"type":"model.reasoning_delta"');
  expect(body).not.toContain("event: error\n");
  expect(body).toContain('"type":"done"');
  expect(body).toContain('"status":"canceled"');
  const recovered = page.getByRole("region", { name: "Recovered output" });
  await expect(recovered).toContainText("Provider reasoning");
  await expect(recovered).toContainText("Incomplete");
  await expect(recovered).toContainText("DISPLAY_ONLY_REASONING");
  expect(await recovered.innerText()).not.toContain("sk-fixture");
  const replay = await evidence(page, request, prompt);
  expect(replay.run.status).toBe("canceled");
  expect(replay.run_events.filter((event: { type: string }) => event.type === "run.canceled")).toHaveLength(1);
  expect(replay.run_events.some((event: { type: string }) => event.type === "run.failed")).toBe(false);
  const saved = replay.projection.partial_outputs;
  expect(saved.some((item: { channel: string; text: string; status: string }) =>
    item.channel === "reasoning" && item.status === "interrupted" && item.text.includes("DISPLAY_ONLY_REASONING"))).toBe(true);
  await expect(page.locator(".error").filter({ hasText: "Internal Server Error" })).toHaveCount(0);
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("Next task.");
  await expect(page.getByRole("button", { name: "Send message", exact: true })).toBeEnabled();
  await test.info().attach("run-cancellation-evidence", { body: JSON.stringify({
    mode, prompt, run_id: replay.run.id, status: replay.run.status,
    event_types: replay.run_events.map((event: { type: string }) => event.type),
    terminal_stream: "done:canceled", error_frame: false,
    partial_outputs: saved, checkpoint_observed_before_stop: true,
    limitations: ["local deterministic provider; real browser, Go composition and isolated Postgres"]
  }, null, 2), contentType: "application/json" });
  await page.reload();
  await expect(page.getByLabel("Task status: canceled", { exact: true })).toBeVisible();
  await expect(recovered).toContainText("Incomplete");
  await expect(recovered).toContainText("DISPLAY_ONLY_REASONING");
  expect(await recovered.innerText()).not.toContain("sk-fixture");
  await expect(page.locator(".error").filter({ hasText: "Internal Server Error" })).toHaveCount(0);
});
}

test("budget exhaustion cannot turn received reasoning into a successful answer", async ({ page, request }) => {
  const prompt = "reasoning-budget: exhaust completion usage";
  await submit(page, prompt);
  await expect(page.getByLabel("Task status: failed", { exact: true })).toBeVisible();
  const recovered = page.getByRole("region", { name: "Recovered output" });
  await expect(recovered).toContainText("Incomplete");
  expect(await recovered.innerText()).not.toContain(secret);
  const replay = await evidence(page, request, prompt);
  expect(replay.run.status).toBe("failed");
  expect(replay.run.error).toContain("budget");
  await expect(page.getByText("Working...", { exact: true })).toHaveCount(0);
});

test("large reasoning is explicitly truncated without changing the final answer", async ({ page, request }) => {
  const prompt = "reasoning-cap: long provider explanation";
  await submit(page, prompt);
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
  const disclosure = page.locator(".provider-reasoning");
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Display truncated");
  expect(new TextEncoder().encode(await disclosure.locator("pre").innerText()).length).toBeLessThanOrEqual(16384);
  await evidence(page, request, prompt);
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await expect(disclosure).toHaveCount(0);
});

test("absent and empty reasoning do not create empty panels", async ({ page }) => {
  for (const prompt of ["reasoning-empty: empty field", "ordinary answer: absent reasoning"]) {
    await submit(page, prompt);
    await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
    await expect(page.locator(".provider-reasoning")).toHaveCount(0);
  }
});
