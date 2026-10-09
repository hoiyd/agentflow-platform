import { expect, test } from "@playwright/test";

const api = "http://127.0.0.1:18080";
const web = "http://127.0.0.1:13000";
test.skip(process.env.AGENTFLOW_IDENTITY_TEST !== "1", "requires signed OIDC and disposable Postgres");

test("Workspace Agent copies and Tool switches are private, durable and usable from Chat", async ({ page, browser }, info) => {
  await page.goto("/workspace");
  await page.getByRole("button", {name: "Sign in", exact: true}).click();
  await expect(page.getByRole("button", {name: /^Workspace: /})).toBeVisible();
  const firstWorkspaceName = (await page.getByRole("button", {name: /^Workspace: /}).getAttribute("aria-label"))!.replace(/^Workspace: /, "");
  const session = await (await page.request.get(`${api}/api/auth/session`)).json();
  const headers = {Origin: web, "X-Workspace-ID": session.personal_workspace};
  await page.getByRole("button", {name: "New conversation", exact: true}).click();
  await page.getByRole("button", {name: "Direct Single agent", exact: true}).click();
  const initialAgents = await (await page.request.get(`${api}/api/agents`, {headers})).json();
  const template = initialAgents.find((item: {is_template: boolean}) => item.is_template);
  await page.getByRole("button", {name: "New agent", exact: true}).click();
  const dialog = page.getByRole("dialog", {name: "Create new agent"});
  await expect(dialog.getByLabel("Name", {exact: true})).toHaveValue("");
  await dialog.getByRole("button", {name: "Copy from: Blank agent", exact: true}).click();
  const image = info.outputPath("agent-creation-template-picker.png");
  await page.screenshot({path: image});
  await info.attach("agent-creation-template-picker.png", {path: image, contentType: "image/png"});
  await dialog.getByRole("option").filter({hasText: template.name}).first().click();
  await expect(dialog.getByRole("textbox", {name: "System prompt", exact: true})).toHaveValue(template.system_prompt);
  await expect(dialog.getByRole("textbox", {name: "Description", exact: true})).toHaveValue(template.description);
  // Choosing a template is local draft state, not an implicit create request.
  expect(await (await page.request.get(`${api}/api/agents`, {headers})).json()).toEqual(initialAgents);
  await dialog.getByLabel("Name", {exact: true}).fill("Private Workspace writer");
  await dialog.getByLabel("Memory retrieval", {exact: true}).uncheck();
  await dialog.getByLabel("Knowledge retrieval", {exact: true}).uncheck();
  await dialog.getByRole("button", {name: "Create Agent", exact: true}).click();
  await page.getByRole("button", {name: "OK", exact: true}).click();
  const agents = await (await page.request.get(`${api}/api/agents`, {headers})).json();
  const agent = agents.find((item: {name: string}) => item.name === "Private Workspace writer");
  expect(agent.workspace_id).toBe(session.personal_workspace);
  expect(agent.is_template).toBe(false);
  expect(agent.tools).toEqual(template.tools);
  expect(agent.skills ?? []).toEqual(template.skills ?? []);
  expect(agent.routing_hints).toEqual(template.routing_hints);
  expect(agents.find((item: {id: string}) => item.id === template.id)).toEqual(template);
  await page.getByRole("button", {name: /^Agent: /}).click();
  const options = page.getByRole("listbox", {name: "Agent", exact: true}).getByRole("option");
  await expect(options).toHaveCount(agents.filter((item: {is_template: boolean}) => !item.is_template).length);
  await page.getByRole("combobox", {name: "Search agents"}).press("Escape");
  expect((await page.request.patch(`${api}/api/agents/${template.id}`, {headers, data: {name: "Forbidden edit"}})).status()).toBe(403);
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("Workspace configuration evidence");
  await page.getByRole("button", {name: "Send message", exact: true}).click();
  await expect(page.getByText("Evidence saved.", {exact: true})).toBeVisible();
  await page.getByRole("button", {name: "Tools", exact: true}).click();
  const toggle = page.getByRole("checkbox", {name: "Allow calculator in this Workspace", exact: true});
  await expect(toggle).toBeChecked();
  await toggle.click();
  await expect(toggle).not.toBeChecked();
  await page.reload();
  await page.getByRole("button", {name: "Tools", exact: true}).click();
  await expect(page.getByRole("checkbox", {name: "Allow calculator in this Workspace", exact: true})).not.toBeChecked();
  const secondResponse = await page.request.post(`${api}/api/workspaces`, {headers: {Origin: web}, data: {name: "Owner's second configuration"}});
  expect(secondResponse.status()).toBe(201);
  const secondSpace = await secondResponse.json();
  const secondHeaders = {...headers, "X-Workspace-ID": secondSpace.id};
  expect((await page.request.get(`${api}/api/agents/${agent.id}`, {headers: secondHeaders})).status()).toBe(404);
  await page.reload();
  await page.getByRole("button", {name: /^Workspace: /}).click();
  await page.getByRole("option", {name: "Owner's second configuration", exact: true}).click();
  await page.getByRole("button", {name: "Direct Single agent", exact: true}).click();
  await expect(page.getByRole("button", {name: "Send message", exact: true})).toBeDisabled();
  await expect(page.getByRole("button", {name: "New agent", exact: true})).toBeEnabled();
  await page.getByRole("button", {name: "New agent", exact: true}).click();
  await page.getByRole("dialog", {name: "Create new agent"}).getByRole("button", {name: "Cancel create new agent", exact: true}).click();
  expect((await (await page.request.get(`${api}/api/agents`, {headers: secondHeaders})).json()).every((item: {is_template: boolean}) => item.is_template)).toBe(true);
  await page.getByRole("button", {name: "Tools", exact: true}).click();
  await expect(page.getByRole("checkbox", {name: "Allow calculator in this Workspace", exact: true})).toBeChecked();
  await page.getByRole("button", {name: /^Workspace: /}).click();
  await page.getByRole("option", {name: firstWorkspaceName, exact: true}).click();
  await page.getByRole("button", {name: "Tools", exact: true}).click();
  await expect(page.getByRole("checkbox", {name: "Allow calculator in this Workspace", exact: true})).not.toBeChecked();
  const otherContext = await browser.newContext();
  try {
    await page.request.post(`${api}/__fixture/identity/subject`, {data: {subject: "fixture-agent-stranger"}});
    const other = await otherContext.newPage();
    await other.goto(`${web}/workspace`);
    await other.getByRole("button", {name: "Sign in", exact: true}).click();
    await expect(other.getByRole("button", {name: /^Workspace: /})).toBeVisible();
    const otherSession = await (await other.request.get(`${api}/api/auth/session`)).json();
    const otherHeaders = {Origin: web, "X-Workspace-ID": otherSession.personal_workspace};
    const otherSecondResponse = await other.request.post(`${api}/api/workspaces`, {headers: {Origin: web}, data: {name: "Other owner's second configuration"}});
    expect(otherSecondResponse.status()).toBe(201);
    const otherSecond = await otherSecondResponse.json();
    for (const method of ["get", "patch", "delete"] as const) {
      expect((await other.request[method](`${api}/api/agents/${agent.id}`, {headers: otherHeaders, ...(method === "patch" ? {data: {name: "Stolen"}} : {})})).status()).toBe(404);
    }
    expect((await other.request.get(`${api}/api/agents`, {headers})).status()).toBe(404);
    const otherTools = await (await other.request.get(`${api}/api/tools`, {headers: otherHeaders})).json();
    expect(otherTools.find((item: {name: string}) => item.name === "calculator").workspace_enabled).toBe(true);
    for (const workspace of [otherSession.personal_workspace, otherSecond.id]) {
      const scope = {...otherHeaders, "X-Workspace-ID": workspace};
      expect((await other.request.get(`${api}/api/agents/${agent.id}`, {headers: scope})).status()).toBe(404);
      const before = await (await other.request.get(`${api}/api/conversations`, {headers: scope})).json();
      for (const mode of ["single", "multi_agent", "autonomous"]) {
        const foreign = await other.request.post(`${api}/api/chat`, {headers: scope, data: {agent_id: agent.id, message: "Foreign Agent", mode}});
        const after = await (await other.request.get(`${api}/api/conversations`, {headers: scope})).json();
        expect(after, `${mode} must reject before creating a Conversation`).toEqual(before);
        expect(foreign.status()).toBe(404);
        expect(await foreign.text()).toContain("agent not found");
      }
    }
    const foreignRuns = await (await other.request.get(`${api}/api/runs`, {headers: otherHeaders})).json();
    expect(foreignRuns.some((run: {agent_id: string}) => run.agent_id === agent.id)).toBe(false);
    await info.attach("workspace-agent-config-evidence.json", {body: JSON.stringify({store: "disposable-postgres", owners: [session.user.id, otherSession.user.id], workspaces: [session.personal_workspace, secondSpace.id, otherSession.personal_workspace, otherSecond.id], agent: agent.id, template: template.id, checks: ["OIDC owner creates Agent from UI", "Copy from defaults to blank and only updates draft until save", "prompt, description, routing hints, Tools and Skills copied", "template unchanged and mutation denied", "Agent picker excludes templates", "empty Workspace can create/cancel but cannot send Single Chat", "Chat executes owned Agent", "Tool toggle persists reload and Workspace switching", "other owner cannot read/update/archive/use Agent", "foreign Agent rejected in all modes without creating a Conversation or Run", "other Workspace Tool grant unchanged"], limitations: ["desktop only", "deterministic provider and signed OIDC fixtures"]}, null, 2), contentType: "application/json"});
  } finally {
    await otherContext.close();
    await page.request.post(`${api}/__fixture/identity/subject`, {data: {subject: "fixture-operator"}});
  }
});
