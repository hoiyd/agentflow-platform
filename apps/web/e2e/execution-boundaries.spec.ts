import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";
test.skip(process.env.AGENTFLOW_EXECUTION_BOUNDARY_TEST !== "1", "requires isolated OIDC and governed HTTP fixtures");

test("HTTP boundaries and OIDC command denial survive persistence and reload", async ({ page }, info) => {
  const browserErrors: string[] = [];
  page.on("pageerror", error => browserErrors.push(error.message));
  await page.goto("/workspace");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  const session = await (await page.request.get(`${api}/api/auth/session`)).json();
  const headers = { Origin: "http://127.0.0.1:13000", "X-Workspace-ID": session.personal_workspace };
  const agent = await createWorkspaceChatAgent(page, session.personal_workspace, "Execution boundary Agent");
  const read = async (path: string) => {
    const response = await page.request.get(api + path, { headers });
    expect(response.ok(), path).toBe(true);
    return response.json();
  };
  const fixture = await read("/__fixture/execution-boundary");
  expect(fixture.auth_mode).toBe("oidc");
  const evidence = [];
  for (const scenario of [
    { name: "allowed", url: fixture.allowed_origin, status: "passed", reason: undefined },
    { name: "ungranted port", url: fixture.forbidden_origin, status: "blocked", reason: "policy_denied" },
    { name: "redirect", url: fixture.allowed_origin + "/redirect", status: "blocked", reason: "policy_denied" },
    { name: "oversized", url: fixture.allowed_origin + "/oversized", status: "blocked", reason: "execution_failed" }
  ]) {
    await page.getByRole("button", { name: "New conversation", exact: true }).click();
    await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
    await page.getByRole("button", { name: /^Verification/ }).click();
    const dialog = page.getByRole("dialog", { name: "Verification" });
    await dialog.getByLabel("Disabled", { exact: true }).check();
    await dialog.getByRole("combobox", { name: "Maximum attempts", exact: true }).selectOption("1");
    await dialog.getByRole("combobox", { name: "When attempts are exhausted", exact: true }).selectOption("fail");
    await dialog.getByLabel("Use text constraints", { exact: true }).uncheck();
    await dialog.getByRole("tab", { name: "HTTP", exact: true }).click();
    await dialog.getByLabel("Use HTTP check", { exact: true }).check();
    await dialog.getByLabel("URL", { exact: true }).fill(scenario.url);
    await dialog.getByRole("button", { name: "Save policy", exact: true }).click();
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(`execution-boundary: ${scenario.name}`);
    const [sent] = await Promise.all([
      page.waitForRequest(r => r.url() === `${api}/api/chat` && r.method() === "POST"),
      page.getByRole("button", { name: "Send message", exact: true }).click()
    ]);
    expect(sent.postDataJSON().agent_id).toBe(agent.id);
    const contract = sent.postDataJSON().completion_contract;
    expect(contract.verifiers).toHaveLength(1);
    expect(contract.verifiers[0].config.url).toBe(scenario.url);
    await expect(page.getByLabel(`Task status: ${scenario.status === "passed" ? "completed" : "failed"}`, { exact: true })).toBeVisible();
    const href = (await page.getByRole("link", { name: "View trace" }).getAttribute("href"))!;
    const runId = href.split("/").at(-1)!;
    const replay = await read(`/api/runs/${runId}/replay`);
    expect(replay.run.workspace_id).toBe(session.personal_workspace);
    expect(replay.run.agent_id).toBe(agent.id);
    expect(replay.run.verification_status).toBe(scenario.status);
    expect(replay.verification_evidence[0].status).toBe(scenario.status);
    expect(replay.verification_evidence[0].details.reason_code).toBe(scenario.reason);
    await page.goto(href);
    await expect(page.locator("#run-verification-evidence")).toContainText(scenario.status);
    await page.reload();
    await expect(page.locator("#run-verification-evidence")).toContainText(scenario.status);
    evidence.push({ scenario: scenario.name, contract, run: replay.run, verification: replay.verification_evidence });
    await page.goto("/workspace");
    await expect(page.getByText("API connected", { exact: true })).toBeVisible();
  }
  await page.getByRole("button", { name: "New conversation", exact: true }).click();
  await page.getByRole("button", { name: "Direct Single agent", exact: true }).click();
  await page.getByRole("button", { name: /^Verification/ }).click();
  const dialog = page.getByRole("dialog", { name: "Verification" });
  await dialog.getByRole("tab", { name: "Command", exact: true }).click();
  await expect(dialog.getByLabel("Use command check", { exact: true })).toBeDisabled();
  await expect(dialog).toContainText("requires an isolated runner");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  // Bypass the UI to prove the backend enforces the same rule. Origin/session
  // and owner scope remain real; only the candidate model response is a fixture.
  const response = await page.request.post(`${api}/api/chat`, {
    headers,
    data: { agent_id: agent.id, message: "execution-boundary: reject host execution", mode: "single", completion_contract: {
      verifiers: [{ id: "host-command", type: "command", required: true, config: { args: ["/usr/bin/touch", fixture.command_marker] } }],
      policy: { mode: "all_must_pass", max_attempts: 1, on_exhausted: "fail" }
    } }
  });
  expect(response.ok()).toBe(true);
  const stream = await response.text();
  expect(stream).toContain('"verification_status":"blocked"');
  const identity = /"run_id":"(run_[^"]+)"/.exec(stream);
  expect(identity).not.toBeNull();
  const denied = await read(`/api/runs/${identity![1]}/replay`);
  expect(denied.run.agent_id).toBe(agent.id);
  expect(denied.run.workspace_id).toBe(session.personal_workspace);
  expect(denied.verification_evidence[0].details.reason_code).toBe("policy_denied");
  const final = await read("/__fixture/execution-boundary");
  expect(final.allowed_requests).toBe(3);
  expect(final.forbidden_requests).toBe(0);
  expect(final.command_executed).toBe(false);
  expect(browserErrors).toEqual([]);
  await info.attach("execution-boundary-evidence.json", {
    body: JSON.stringify({ schema: "execution-boundary-evidence-v1", auth: "signed-oidc-fixture", persistence: "disposable-postgres",
      workspace: session.personal_workspace, agent: agent.id,
      scenarios: evidence, command: { run: denied.run, verification: denied.verification_evidence }, observed: final,
      limitations: ["DNS rebinding and subprocess cleanup covered separately by isolated backend tests", "not an OS sandbox", "not live provider quality"] }, null, 2),
    contentType: "application/json"
  });
});
