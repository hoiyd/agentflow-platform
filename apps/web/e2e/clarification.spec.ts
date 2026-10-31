import { test, expect } from "@playwright/test";
import { createWorkspaceChatAgent, defaultWorkspaceID } from "./fixtures/workspace-agent";
const api="http://127.0.0.1:18080";
let createdAgent:{id:string;workspace:string}|undefined;
test.afterEach(async({request})=>{
 if(createdAgent){const response=await request.delete(`${api}/api/agents/${createdAgent.id}`,{headers:{"X-Workspace-ID":createdAgent.workspace}});expect(response.ok()).toBe(true);createdAgent=undefined;}
});

for(const mode of ["Single agent","Multi-agent","Bounded loop"]){
 test(`durable clarification: ${mode}`,async({page,request})=>{
  const errors:string[]=[];page.on("pageerror",error=>errors.push(error.message));
  await page.goto("/workspace");await expect(page.getByText("API connected",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"New conversation",exact:true}).click();
  await page.getByRole("button",{name:"Direct Single agent",exact:true}).click();
  const workspace=await defaultWorkspaceID(page);
  const capability=`clarification${mode.replace(/[^a-z]/gi,"").toLowerCase()}`;
  const agent=await createWorkspaceChatAgent(page,workspace,`Clarification ${mode}`,{system_prompt:"Prepare audience-specific reports. Ask necessary questions.",routing_hints:{capabilities:["report",capability],task_examples:["clarification-gate: prepare a report, audience unspecified"],exclusions:[]}});
  createdAgent={id:agent.id,workspace};
  await page.getByRole("region",{name:"Chat mode",exact:true}).getByRole("button",{name:new RegExp(mode)}).click();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("clarification-gate: prepare a report, audience unspecified");
  await page.getByRole("button",{name:"Send message",exact:true}).click();
  const panel=page.getByRole("region",{name:"Run clarification",exact:true});
  await expect(panel.getByText("Who is the audience?",{exact:true})).toBeVisible();
  if(mode==="Multi-agent")await expect(page.getByRole("button",{name:"Approve & Continue"})).toHaveCount(0);
  const runId=(await page.getByRole("link",{name:"View trace"}).getAttribute("href"))!.split("/").at(-1)!;
  const read=async(path:string)=>{const response=await request.get(`${api}${path}`,{headers:{"X-Workspace-ID":workspace}});expect(response.ok()).toBe(true);return response.json();};
  const initial=await read(`/api/runs/${runId}`);
  const initialReplay=await read(`/api/runs/${runId}/replay`);
  const hydrationChecks:string[]=[];
  if(mode==="Multi-agent"){
   const posts:string[]=[];
   page.on("request",req=>{if(req.method()==="POST"&&req.url().includes(`/api/runs/${runId}/`))posts.push(req.url());});
   let release!:()=>void;
   const held=new Promise<void>(resolve=>{release=resolve;});
   let reads=0;
   const clarificationURL=`**/api/runs/${runId}/clarifications`;
   await page.route(clarificationURL,async route=>{
    if(route.request().method()!=="GET"){await route.continue();return;}
    const response=await route.fetch();
    expect(response.ok()).toBe(true);
    reads++;
    await held;
    await route.fulfill({response});
   });
   await page.reload();
   await expect.poll(()=>reads).toBeGreaterThan(0);
   await expect(page.getByRole("button",{name:"Approve & Continue"})).toHaveCount(0);
   await expect(page.getByRole("region",{name:"Review generated plan"})).toHaveCount(0);
   await expect(panel).toHaveCount(0);
   expect(posts).toEqual([]);
   await test.info().attach("recovery-hydration-loading.png",{body:await page.screenshot(),contentType:"image/png"});
   release();
   await expect(panel.getByText("Who is the audience?",{exact:true})).toBeVisible();
   await page.unroute(clarificationURL);
   await page.route(clarificationURL,async route=>{await route.abort("failed");});
   await page.reload();
   await expect(page.locator(".chat-workspace").getByRole("alert")).toBeVisible();
   await expect(page.getByRole("button",{name:"Approve & Continue"})).toHaveCount(0);
   await expect(page.getByRole("region",{name:"Review generated plan"})).toHaveCount(0);
   expect(posts).toEqual([]);
   const persisted=await read(`/api/runs/${runId}/clarifications`);
   expect(persisted[0].status).toBe("pending");
   expect((await read(`/api/runs/${runId}`)).status).toBe("waiting_for_user");
   await test.info().attach("recovery-hydration-error.png",{body:await page.screenshot(),contentType:"image/png"});
   await page.unroute(clarificationURL);
   await page.reload();
   hydrationChecks.push("delayed real clarification read blocks continuation","failed hydration hides approval and empty pause sections while preserving the error","rejected read cannot issue recovery POST","failed hydration preserves waiting Run and pending question","successful reload rehydrates question");
  }else await page.reload();
  await expect(panel.getByText("Who is the audience?",{exact:true})).toBeVisible();
  if(mode==="Single agent"){
   await panel.getByLabel("Your answer",{exact:true}).fill("字".repeat(3000));
   await panel.getByRole("button",{name:"Answer and continue",exact:true}).click();
   await expect(page.locator(".composer .error")).toContainText("invalid clarification questions or answers");
   expect((await read(`/api/runs/${runId}/clarifications`))[0].status).toBe("pending");
   await expect(panel.getByText("Who is the audience?",{exact:true})).toBeVisible();
  }
  await panel.getByRole("radio",{name:"Engineers",exact:true}).check();
  await panel.getByLabel("Your answer",{exact:true}).fill("Engineering leads");
  await panel.getByRole("button",{name:"Answer and continue",exact:true}).click();
  if(mode==="Multi-agent"){
   await page.getByText("Routing requirements",{exact:true}).click();
   await page.getByRole("textbox",{name:"Preferred capabilities",exact:true}).fill(capability);
   await page.getByRole("button",{name:"Approve & Continue",exact:true}).click();
  }
  await expect.poll(async()=> (await read(`/api/runs/${runId}`)).status).toBe("completed");
  await expect(page.locator(".message.assistant").last()).toContainText("Engineering leads");
  const questions=await read(`/api/runs/${runId}/clarifications`);
  expect(questions).toHaveLength(1);expect(questions[0].status).toBe("applied");expect(questions[0].answers.audience).toBe("Engineering leads");
  const replay=await read(`/api/runs/${runId}/replay`);expect(replay.runtime_snapshot).toEqual(initialReplay.runtime_snapshot);expect(replay.projection.invariant_failures??[]).toEqual([]);
  const messages=await read(`/api/conversations/${initial.conversation_id}/messages`);expect(messages.filter((m:{id:string})=>m.id===questions[0].id)).toHaveLength(1);
  await page.goto(`/runs/${runId}`);await page.getByText("Clarifications (1)",{exact:true}).click();await expect(page.getByRole("region",{name:"Run clarification"})).toContainText("Engineering leads");
  expect(errors).toEqual([]);
  await test.info().attach("clarification-evidence.json",{body:JSON.stringify({schema:"durable-clarification-browser-v1",mode,run_id:runId,snapshot:replay.runtime_snapshot,clarifications:questions,hydration_checks:hydrationChecks,checks:["browser -> Go -> Postgres","reload pending question","free text with options","same Run and Snapshot","answer persisted once","Replay receipts","no runtime invariant failures"],limitations:["deterministic Provider fixture, not live model compliance"]},null,2),contentType:"application/json"});
 });
}
