import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";

const api = "http://127.0.0.1:18080";

for (const rejected of [false, true]) {
test(`History handoff preserves drafts and ${rejected ? "rejection recovery" : "streamed delivery"}`, async ({ page, request }, info) => {
  await page.goto("/workspace");
  const workspace = await defaultWorkspaceID(page);
  const agent = await createWorkspaceChatAgent(page, workspace, `History ownership ${rejected ? "rejection" : "delivery"} fixture`);
  const headers = { "X-Workspace-ID": workspace, Origin: "http://127.0.0.1:13000" };
  const created = await request.post(`${api}/api/conversations`, { headers, data: { title: "History ownership evidence" } });
  expect(created.status()).toBe(201);
  const conversation = await created.json();
  const baseline = "Retained baseline for history ownership";
  const seeded = await request.post(`${api}/api/chat`, { headers, data: { conversation_id: conversation.id, agent_id: agent.id, mode: "single", message: baseline } });
  expect(seeded.ok()).toBe(true);
  expect(await seeded.text()).not.toContain("event: error");
  let releaseHistory!: () => void, historyArrived!: () => void, historyDone!: () => void;
  let releaseSubmit!: () => void, submitArrived!: () => void;
  const historyGate = new Promise<void>(resolve => { releaseHistory = resolve; });
  const historyReady = new Promise<void>(resolve => { historyArrived = resolve; });
  const historySettled = new Promise<void>(resolve => { historyDone = resolve; });
  const submitGate = new Promise<void>(resolve => { releaseSubmit = resolve; });
  const submitReady = new Promise<void>(resolve => { submitArrived = resolve; });
  await page.route(`**/api/conversations/${conversation.id}/messages`, async route => {
    const response = await route.fetch();
    historyArrived();
    await historyGate;
    try { await route.fulfill({ response }); } finally { historyDone(); }
  }, { times: 1 });
  let interceptSubmit = true;
  await page.route("**/api/chat", async route => {
    if (!interceptSubmit) { await route.continue(); return; }
    interceptSubmit = false;
    submitArrived();
    await submitGate;
    if (rejected) await route.fulfill({ status: 503, json: { error: "Synthetic pre-acceptance rejection" } });
    else await route.continue();
  });
  const prompt = `History handoff ${rejected ? "retry" : "delivery"} task`;
  try {
    await page.goto(`/workspace?conversation=${conversation.id}`);
    await historyReady;
    await page.getByRole("region", {name:"Chat mode"}).getByRole("button", {name:"Direct Single agent", exact:true}).click();
    await page.getByPlaceholder("Ask AgentFlow anything...").fill(prompt);
    await page.getByRole("button", {name:"Send message", exact:true}).click();
    await submitReady;
    releaseHistory();
    await historySettled;
    await expect(page.getByText(prompt, {exact:true})).toBeVisible();
    await info.attach("history-draft-before-acceptance.png", {body:await page.screenshot(),contentType:"image/png"});
    releaseSubmit();
    if (rejected) {
      await expect(page.locator(".composer .error")).toContainText("Synthetic pre-acceptance rejection");
      await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue(prompt);
      await expect(page.getByText(baseline,{exact:true})).toBeVisible();
      await expect(page.locator(".message.user .bubble").filter({hasText:prompt})).toHaveCount(0);
      await page.getByRole("button",{name:"Send message",exact:true}).click();
    }
    await expect(page.getByLabel("Task status: completed",{exact:true})).toBeVisible();
    const messages = await (await request.get(`${api}/api/conversations/${conversation.id}/messages`,{headers})).json();
    expect(messages.filter((message:{role:string;content:string})=>message.role==="user"&&message.content===prompt)).toHaveLength(1);
    const answer=messages.filter((message:{role:string})=>message.role==="assistant").at(-1);
    expect(answer.content).toBeTruthy();
    await expect(page.locator(".message.assistant .bubble").filter({hasText:answer.content}).last()).toBeVisible();
    await page.reload();
    await expect(page.getByText(prompt,{exact:true})).toBeVisible();
    await info.attach("history-handoff-evidence.json",{contentType:"application/json",body:JSON.stringify({workspace_id:workspace,agent_id:agent.id,conversation_id:conversation.id,prompt,rejected,messages,checks:["late real history held across Submit","draft retained before SSE","real Go completion","one persisted user message","reload"],limitations:["controlled provider","only rejection is injected; successful delivery uses production composition"]},null,2)});
  } finally { releaseHistory(); releaseSubmit(); }
});
}
