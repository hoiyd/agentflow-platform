import { expect, test } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";

// The successful path uses real HTTP, Runtime and Postgres. Only read degradation
// and a pre-admission rejection are injected; neither may erase accepted facts.
test("Agent changes preserve accepted Run facts and only configure the next submission", async ({ page, request }, info) => {
  const errors: string[] = [];
  page.on("pageerror", error => errors.push(error.message));
  await page.goto("/workspace");
  const workspace = await defaultWorkspaceID(page);
  const first = await createWorkspaceChatAgent(page, workspace, "Run facts original");
  const second = await createWorkspaceChatAgent(page, workspace, "Run facts next");
  const headers = {"X-Workspace-ID":workspace, Origin:"http://127.0.0.1:13000"};
  const created = await request.post(`${api}/api/conversations`, {headers, data:{title:"Run facts fixture"}});
  expect(created.status()).toBe(201);
  const conversation = await created.json();
  const baseline = "Preserve accepted Run facts";
  const seeded = await request.post(`${api}/api/chat`, {headers, data:{conversation_id:conversation.id, agent_id:first.id, mode:"single", message:baseline}});
  expect(seeded.ok()).toBe(true);
  expect(await seeded.text()).not.toContain("event: error");
  const runs = await (await request.get(`${api}/api/runs`, {headers})).json();
  const original = runs.find((run:{conversation_id:string}) => run.conversation_id === conversation.id);
  expect(original).toMatchObject({mode:"single", status:"completed", agent_id:first.id});
  await page.goto(`/workspace?conversation=${conversation.id}`);
  const trace = page.getByRole("link", {name:"View trace", exact:true});
  const assertAccepted = async () => {
    await expect(trace).toHaveAttribute("href", `/runs/${original.id}`);
    await expect(page.getByLabel("Task status: completed", {exact:true})).toBeVisible();
    await expect(page.getByText(baseline, {exact:true})).toBeVisible();
    await expect(page.getByLabel("Run inbox", {exact:true})).toHaveCount(1);
  };
  await assertAccepted();
  await page.getByRole("button", {name:/^Agent: /}).click();
  await page.getByRole("option", {name:second.name, exact:true}).click();
  await assertAccepted();

  await page.getByRole("button", {name:"New agent", exact:true}).click();
  const create = page.getByRole("dialog", {name:"Create new agent"});
  await create.getByLabel("Name", {exact:true}).fill("Run facts created");
  await create.getByLabel("Memory retrieval", {exact:true}).uncheck();
  await create.getByLabel("Knowledge retrieval", {exact:true}).uncheck();
  await create.getByRole("button", {name:"Create Agent", exact:true}).click();
  await page.getByRole("button", {name:"OK", exact:true}).click();
  await assertAccepted();
  await page.getByRole("button", {name:"Configure", exact:true}).click();
  await page.getByRole("dialog", {name:"Edit agent config"}).getByRole("button", {name:"Archive Agent", exact:true}).click();
  await page.getByRole("dialog").filter({has:page.getByRole("heading", {name:"Run facts created", exact:true})}).getByRole("button", {name:"Archive Agent", exact:true}).click();
  await page.getByRole("button", {name:"OK", exact:true}).click();
  await assertAccepted();

  await page.getByRole("button", {name:/^Agent: /}).click();
  await page.getByRole("option", {name:second.name, exact:true}).click();
  await page.route("**/api/chat", route => route.fulfill({status:503, json:{error:"Synthetic admission rejection"}}), {times:1});
  const prompt = "Use the next Agent without losing the original Run";
  await page.getByPlaceholder("Ask AgentFlow anything...").fill(prompt);
  await page.getByRole("button", {name:"Send message", exact:true}).click();
  await expect(page.locator(".composer .error")).toContainText("Synthetic admission rejection");
  await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue(prompt);
  await assertAccepted();
  await info.attach("accepted-run-after-agent-changes.png", {body:await page.screenshot(), contentType:"image/png"});
  await page.getByRole("button", {name:"Send message", exact:true}).click();
  await expect(trace).not.toHaveAttribute("href", `/runs/${original.id}`);
  await expect(page.getByLabel("Task status: completed", {exact:true})).toBeVisible();
  const nextID = (await trace.getAttribute("href"))!.split("/").at(-1)!;
  const next = await (await request.get(`${api}/api/runs/${nextID}`, {headers})).json();
  expect(next).toMatchObject({mode:"single", agent_id:second.id, status:"completed"});
  await page.reload();
  await expect(trace).toHaveAttribute("href", `/runs/${next.id}`);
  expect(errors).toEqual([]);
  await info.attach("run-facts-evidence.json", {contentType:"application/json", body:JSON.stringify({workspace_id:workspace, conversation_id:conversation.id, original, next, baseline, prompt, checks:["select/create/archive preserve accepted Run and inbox", "rejection restores Run and input", "real accepted submit uses next Agent", "persisted mode and reload"], limitations:["deterministic provider", "injected rejection only", "desktop only"]}, null, 2)});
});

for (const mode of ["single", "multi_agent", "autonomous"] as const) {
  test(`history restores frozen ${mode} mode despite empty, unknown or failed Stage reads`, async ({page, request}, info) => {
    await page.goto("/workspace");
    const workspace = await defaultWorkspaceID(page);
    const agent = await createWorkspaceChatAgent(page, workspace, `Frozen ${mode}`);
    const headers = {"X-Workspace-ID":workspace, Origin:"http://127.0.0.1:13000"};
    const created = await request.post(`${api}/api/conversations`, {headers, data:{title:`Frozen ${mode} fixture`}});
    expect(created.status()).toBe(201);
    const conversation = await created.json();
    const seeded = await request.post(`${api}/api/chat`, {headers, data:{conversation_id:conversation.id, agent_id:agent.id, mode, message:"Frozen mode recovery fixture"}});
    expect(seeded.ok()).toBe(true);
    expect(await seeded.text()).not.toContain("event: error");
    const runs = await (await request.get(`${api}/api/runs`, {headers})).json();
    const run = runs.find((item:{conversation_id:string}) => item.conversation_id === conversation.id);
    expect(run.mode).toBe(mode);
    expect(run).not.toHaveProperty("runtime_snapshot");
    const label = mode === "single" ? "Direct Single agent" : mode === "multi_agent" ? "Coordinate Multi-agent" : "Autonomous Bounded loop";
    for (const degradation of ["empty", "unknown", "failed"]) {
      await page.route(`**/api/runs/${run.id}/collaboration_steps`, route => route.fulfill(degradation === "failed"
        ? {status:503, json:{error:"Synthetic Stage read failure"}}
        : {json:degradation === "empty" ? [] : [{id:"unknown", run_id:run.id, role:"unknown-role", status:"completed", output:""}]}), {times:1});
      await page.goto(`/workspace?conversation=${conversation.id}`);
      await expect(page.getByRole("link", {name:"View trace", exact:true})).toHaveAttribute("href", `/runs/${run.id}`);
      await expect(page.getByRole("region", {name:"Chat mode"}).getByRole("button", {name:label, exact:true})).toHaveClass("active");
      if (degradation === "failed") await expect(page.locator(".composer .error")).toContainText("Failed to load run trace");
    }
    await info.attach(`frozen-${mode}-evidence.json`, {contentType:"application/json", body:JSON.stringify({workspace_id:workspace, conversation_id:conversation.id, run, checks:["empty Stage list", "unknown Stage role", "failed Stage read", "page reload"], limitations:["Stage reads deliberately degraded", "real Run/Snapshot persisted through production composition", "controlled provider"]}, null, 2)});
  });
}
