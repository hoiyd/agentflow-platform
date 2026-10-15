import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";
const schema = { type: "object", properties: { summary: { type: "string" }, facts: { type: "array", items: { type: "string" } } }, required: ["summary", "facts"], additionalProperties: false };

test("structured report contract gates machine results across modes and failures", async ({ page, request }) => {
  test.skip(process.env.AGENTFLOW_STRUCTURED_OUTPUT_TEST !== "1", "requires strict output fixture route");
  test.setTimeout(150_000);
  await page.goto("/workspace");
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const workspace = await defaultWorkspaceID(page);
  const headers = { "X-Workspace-ID": workspace };
  await createWorkspaceChatAgent(page, workspace, "Report extractor", { tools: ["calculator"], routing_hints: { capabilities: ["report"], task_examples: ["structured-output-gate"], exclusions: [] } });
  await page.getByRole("button", { name: /^Verification/ }).click();
  const dialog = page.getByRole("dialog", { name: "Verification" });
  await dialog.getByLabel("Disabled", { exact: true }).check();
  await dialog.getByRole("combobox", { name: "Maximum attempts", exact: true }).selectOption("1");
  await dialog.getByRole("combobox", { name: "When attempts are exhausted", exact: true }).selectOption("fail");
  await dialog.getByLabel("Use text constraints", { exact: true }).uncheck();
  await dialog.getByRole("tab", { name: "JSON Schema", exact: true }).click();
  await dialog.getByLabel("Use JSON Schema", { exact: true }).check();
  await dialog.getByRole("combobox", { name: "Output generation", exact: true }).selectOption("strict");
  await dialog.getByRole("textbox", { name: "JSON Schema 2020-12", exact: true }).fill(JSON.stringify(schema, null, 2));
  await test.info().attach("structured-output-config.png", { body: await page.screenshot(), contentType: "image/png" });
  await dialog.getByRole("button", { name: "Save policy", exact: true }).click();
  const runs = [];
  for (const [mode, outcome] of [["Single agent", "valid"], ["Multi-agent", "valid"], ["Bounded loop", "valid"], ["Single agent", "invalid"], ["Single agent", "mismatch"], ["Single agent", "cutoff"], ["Single agent", "refusal"], ["Single agent", "partial"]] as const) {
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("region", { name: "Chat mode" }).getByRole("button", { name: new RegExp(mode) }).click();
    const sent = page.waitForRequest(r => r.url() === `${api}/api/chat` && r.method() === "POST");
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(`structured-output-gate-${outcome}: Extract supplied material: Project Alpha uses Go. Total 2 + 3 deployments.`);
    await page.getByRole("button", { name: "Send message", exact: true }).click();
    expect((await sent).postDataJSON().completion_contract.output.mode).toBe("strict");
    if (mode === "Multi-agent") {
      await page.getByText("Routing requirements", { exact: true }).click();
      await page.getByRole("textbox", { name: "Preferred capabilities", exact: true }).fill("report");
      await page.getByRole("button", { name: "Approve & Continue", exact: true }).click();
    }
    const trace = page.getByRole("link", { name: "View trace", exact: true });
    await expect(trace).toBeVisible();
    const id = (await trace.getAttribute("href"))!.split("/").at(-1)!;
    await expect.poll(async () => (await (await request.get(`${api}/api/runs/${id}`, { headers })).json()).status, { timeout: 45_000 }).toBe(outcome === "valid" ? "completed" : "failed");
    const response = await request.get(`${api}/api/runs/${id}/result`, { headers });
    expect(response.status()).toBe(outcome === "valid" ? 200 : 409);
    const result = await response.json();
    expect(result.verification_status).toBe(outcome === "valid" ? "passed" : ["invalid", "mismatch"].includes(outcome) ? "failed" : "pending");
    if (outcome === "valid") expect(result.value).toEqual({ summary: "Project Alpha", facts: ["Uses Go", "5 deployments"] });
    else expect(result.value).toBeUndefined();
    const replay = await (await request.get(`${api}/api/runs/${id}/replay`, { headers })).json();
    expect(replay.structured_result).toEqual(result);
    runs.push({ mode, outcome, run_id: id, contract: replay.run.completion_contract, result, events: replay.run_events, evidence: replay.verification_evidence });
  }
  for (const { run_id, mode, outcome } of runs) {
    await page.goto(`/runs/${run_id}`);
    await page.reload();
    await expect(page.getByRole("region", { name: "Structured result", exact: true })).toBeVisible();
    if (outcome === "valid") {
      await page.getByText("Verified JSON · response-schema", { exact: true }).click();
      await expect(page.locator(".structured-result-json")).toContainText("Project Alpha");
      if (mode === "Single agent") await test.info().attach("structured-output-replay.png", { body: await page.screenshot(), contentType: "image/png" });
    }
  }
  const contracts = await (await request.get(`${api}/__fixture/contracts`)).json();
  expect(contracts.failures ?? []).toEqual([]);
  await test.info().attach("structured-output-evidence.json", { body: JSON.stringify({ schema: "structured-output-browser-v1", workspace_id: workspace, input_schema: schema, runs, provider_contracts: contracts, limitations: ["deterministic HTTP provider, not live model quality", "no automatic repair or side-effect rerun"] }, null, 2), contentType: "application/json" });
});
