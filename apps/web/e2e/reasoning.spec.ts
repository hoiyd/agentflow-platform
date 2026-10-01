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

async function evidence(page: Page, request: APIRequestContext, prompt: string) {
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  const runId = href!.split("/").at(-1)!;
  const replay = await read(request, `/api/runs/${runId}/replay`);
  const messages = await read(request, `/api/conversations/${replay.run.conversation_id}/messages`);
  const requests = await read(request, `/api/runs/${runId}/model_requests`);
  const durable = JSON.stringify({ replay, messages, requests });
  expect(durable).not.toContain(secret);
  const reasoningEvents = replay.run_events.filter((item: { type: string }) => item.type === "model.reasoning");
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
    limitations: ["strict local fixture, not live provider evidence", "same-Turn continuation only", "text released after call completion"]
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
    await page.getByRole("button", { name: "New agent", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Create new agent" });
    await dialog.getByLabel("Name", { exact: true }).fill(`Reasoning ${mode}`);
    await dialog.getByRole("textbox", { name: "Description", exact: true }).fill("reasoning-display calculator specialist");
    await dialog.getByRole("textbox", { name: "System prompt", exact: true }).fill("Calculate and explain the result.");
    await dialog.getByText("Routing signals", { exact: true }).click();
    await dialog.getByRole("textbox", { name: "Capabilities", exact: true }).fill(`reasoning${mode.toLowerCase().replace(/[^a-z]/g, "")}`);
    await dialog.getByRole("textbox", { name: "Example tasks", exact: true }).fill("reasoning-display calculate two Tool rounds");
    await dialog.getByLabel("Memory retrieval", { exact: true }).uncheck();
    await dialog.getByLabel("Knowledge retrieval", { exact: true }).uncheck();
    await dialog.getByRole("checkbox", { name: "calculator", exact: true }).check();
    await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
    await page.getByRole("button", { name: "OK", exact: true }).click();
    await page.getByRole("button", { name: new RegExp(mode) }).click();
    const prompt = "reasoning-display: calculate with two Tool rounds";
    await submit(page, prompt);
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("reasoningmultiagent");
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

test("receiving state is real, text is withheld and a browser disconnect does not cancel execution", async ({ page, request }) => {
  const prompt = "reasoning-gate: wait before final answer";
  await submit(page, prompt);
  const disclosure = page.locator(".provider-reasoning");
  await expect(disclosure).toBeVisible();
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Receiving provider reasoning");
  await expect(disclosure.locator("pre")).toHaveCount(0);
  await expect(page.getByText("Working...", { exact: true })).toBeVisible();
  const href = await page.getByRole("link", { name: "View trace" }).getAttribute("href");
  const runId = href!.split("/").at(-1)!;
  await page.getByRole("button", { name: "Tools", exact: true }).click();
  await page.locator(".conversation-item.active").click();
  await expect(disclosure).toBeVisible();
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Receiving provider reasoning");
  await expect(disclosure.locator("pre")).toHaveCount(0);
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
  await evidence(page, request, prompt);
});

test("provider disconnect keeps failure and withholds all partial reasoning", async ({ page, request }) => {
  const prompt = "reasoning-disconnect: interrupt after reasoning";
  await submit(page, prompt);
  await expect(page.getByLabel("Task status: failed", { exact: true })).toBeVisible();
  const disclosure = page.locator(".provider-reasoning");
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Interrupted; text withheld");
  await expect(disclosure.locator("pre")).toHaveCount(0);
  await expect(page.getByText("Working...", { exact: true })).toHaveCount(0);
  expect((await evidence(page, request, prompt)).run.status).toBe("failed");
});

test("explicit cancellation preserves Run state without publishing private partial text", async ({ page, request }) => {
  const prompt = "reasoning-cancel: wait for cancellation";
  await submit(page, prompt);
  const disclosure = page.locator(".provider-reasoning");
  await expect(disclosure).toBeVisible();
  await disclosure.locator("summary").click();
  await expect(disclosure).toContainText("Receiving provider reasoning");
  await page.getByRole("button", { name: "Stop", exact: true }).click();
  await expect(page.getByLabel("Task status: canceled", { exact: true })).toBeVisible();
  await expect(disclosure).toContainText("Run canceled; text withheld");
  await expect(disclosure.locator("pre")).toHaveCount(0);
  expect((await evidence(page, request, prompt)).run.status).toBe("canceled");
});

test("budget exhaustion cannot turn received reasoning into a successful answer", async ({ page, request }) => {
  const prompt = "reasoning-budget: exhaust completion usage";
  await submit(page, prompt);
  await expect(page.getByLabel("Task status: failed", { exact: true })).toBeVisible();
  const disclosure = page.locator(".provider-reasoning");
  await disclosure.locator("summary").click();
  expect(await disclosure.innerText()).not.toContain(secret);
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
