import { expect, test } from "@playwright/test";

test.skip(process.env.AGENTFLOW_SANDBOX_BROWSER_TEST !== "1", "requires the controlled CLI fixture and disposable Postgres");

test("sandbox argument correction, execution, durable receipt and Replay survive reload", async ({ page }, info) => {
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
  const sandboxTool = replay.runtime_snapshot.tools.find((tool: { name: string }) => tool.name === "sandbox_command");
  expect(sandboxTool.parameters.properties.args.prefixItems[0].enum).toEqual(["/bin/sh", "/usr/bin/python3"]);
  const rejected = replay.run_events.filter((event: { type: string; payload: { tool_name: string } }) => event.type === "tool.failed" && event.payload.tool_name === "sandbox_command");
  expect(rejected).toHaveLength(1);
  expect(rejected[0].payload.error_code).toBe("invalid_arguments");
  expect(rejected[0].payload.argument_error).toMatchObject({ code: "enum", path: "/args/0" });
  const completed = replay.run_events.find((event: { type: string; payload: { tool_name: string } }) => event.type === "tool.completed" && event.payload.tool_name === "sandbox_command");
  expect(completed).toBeTruthy();
  expect(JSON.stringify(completed)).toContain('"cleanup_confirmed":true');
  expect(JSON.stringify(completed)).toContain("/usr/bin/python3");
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
    effects: replay.tool_effects, rejected, command: completed, persistence: "disposable-postgres",
    limitations: ["controlled CLI and model fixtures; not real microVM isolation", "single-mode UI; all modes covered by backend HTTP integration"]
  }, null, 2), contentType: "application/json" });
});
