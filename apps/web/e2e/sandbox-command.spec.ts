import { expect, test } from "@playwright/test";

test.skip(process.env.AGENTFLOW_SANDBOX_BROWSER_TEST !== "1", "requires the controlled CLI fixture and disposable Postgres");

test("sandbox tool binding, execution, durable receipt and Replay survive reload", async ({ page }, info) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await page.getByRole("button", { name: "New agent", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Create new agent" });
  await dialog.getByLabel("Name", { exact: true }).fill("Sandbox fixture runner");
  await dialog.getByRole("textbox", { name: "Description", exact: true }).fill("Run a scratch command");
  await dialog.getByRole("textbox", { name: "System prompt", exact: true }).fill("Use sandbox_command and report its receipt.");
  await dialog.getByLabel("sandbox_command", { exact: true }).check();
  await dialog.getByLabel("Memory retrieval", { exact: true }).uncheck();
  await dialog.getByLabel("Knowledge retrieval", { exact: true }).uncheck();
  await dialog.getByRole("button", { name: "Create Agent", exact: true }).click();
  await page.getByRole("button", { name: "OK", exact: true }).click();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("sandbox-gate: run a scratch command");
  await page.getByRole("button", { name: "Send message", exact: true }).click();
  await expect(page.getByLabel("Task status: completed", { exact: true })).toBeVisible();
  await expect(page.getByText("Sandbox receipt saved.", { exact: true })).toBeVisible();
  const href = (await page.getByRole("link", { name: "View trace" }).getAttribute("href"))!;
  const response = await page.request.get(`http://127.0.0.1:18080/api/runs/${href.split("/").at(-1)}/replay`);
  expect(response.ok()).toBe(true);
  const replay = await response.json();
  expect(replay.tool_effects).toHaveLength(1);
  expect(replay.tool_effects[0].tool_name).toBe("sandbox_command");
  expect(replay.tool_effects[0].status).toBe("committed");
  expect(replay.tool_effects[0].turn_id).toBeTruthy();
  expect(replay.runtime_snapshot.tools.some((tool: { name: string; definition_revision: string }) => tool.name === "sandbox_command" && tool.definition_revision)).toBe(true);
  const completed = replay.run_events.find((event: { type: string; payload: { tool_name: string } }) => event.type === "tool.completed" && event.payload.tool_name === "sandbox_command");
  expect(completed).toBeTruthy();
  expect(JSON.stringify(completed)).toContain('"cleanup_confirmed":true');
  await page.goto(href);
  await page.getByRole("button", { name: /^tool\.completed / }).click();
  const payload = page.locator(".raw-json-panel pre");
  await expect(payload).toContainText('"tool_name": "sandbox_command"');
  await expect(payload).toContainText('"cleanup_confirmed": true');
  await page.reload();
  await page.getByRole("button", { name: /^tool\.completed / }).click();
  await expect(payload).toContainText('"tool_name": "sandbox_command"');
  await expect(payload).toContainText('"output": "sandbox browser receipt"');
  expect(errors).toEqual([]);
  await info.attach("sandbox-command-browser-evidence.json", { body: JSON.stringify({
    schema: "sandbox-command-browser-evidence-v1", run: replay.run, snapshot: replay.runtime_snapshot,
    effects: replay.tool_effects, command: completed, persistence: "disposable-postgres",
    limitations: ["controlled CLI and model fixtures; not real microVM isolation", "single-mode UI; all modes covered by backend HTTP integration"]
  }, null, 2), contentType: "application/json" });
});
