import { expect, test } from "@playwright/test";

test.skip(process.env.AGENTFLOW_SANDBOX_BROWSER_TEST !== "1", "requires the controlled Tool batch and disposable Postgres");
const api="http://127.0.0.1:18080";

for(const mode of ["Single agent","Multi-agent","Bounded loop"]) {
 test(`${mode}: durable steering applies after the Tool batch and follow-up gets a fresh Run`,async({page,request})=>{
  expect((await request.post(`${api}/__fixture/tool-progress/block`)).status()).toBe(204);
  const before=(await (await request.get(`${api}/__fixture/contracts`)).json()).failures?.length??0;
  let runId="", followupRunId="";
  try {
   await page.goto("/workspace");
   await expect(page.getByText("API connected",{exact:true})).toBeVisible();
   await page.getByRole("button",{name:"New conversation",exact:true}).click();
   await page.getByRole("button",{name:"Direct Single agent",exact:true}).click();
   await page.getByRole("button",{name:"New agent",exact:true}).click();
   const dialog=page.getByRole("dialog",{name:"Create new agent"});
   const capability=`inbox${mode.replace(/[^a-z]/gi,"").toLowerCase()}`;
   await dialog.getByLabel("Name",{exact:true}).fill(`Inbox ${mode}`);
   await dialog.getByRole("textbox",{name:"System prompt",exact:true}).fill("Run the sandbox fixture and preserve evidence.");
   await dialog.getByLabel("sandbox_command",{exact:true}).check();
   await dialog.getByText("Routing signals",{exact:true}).click();
   await dialog.getByRole("textbox",{name:"Capabilities",exact:true}).fill(capability);
   await dialog.getByLabel("Memory retrieval",{exact:true}).uncheck();
   await dialog.getByLabel("Knowledge retrieval",{exact:true}).uncheck();
   await dialog.getByRole("button",{name:"Create Agent",exact:true}).click();
   await page.getByRole("button",{name:"OK",exact:true}).click();
   await page.getByRole("region",{name:"Chat mode",exact:true}).getByRole("button",{name:new RegExp(mode)}).click();
   await page.getByPlaceholder("Ask AgentFlow anything...").fill("sandbox-gate progress-wait inbox-steer");
   await page.getByRole("button",{name:"Send message",exact:true}).click();
   if(mode==="Multi-agent"){
    await page.getByText("Routing requirements",{exact:true}).click();
    await page.getByRole("textbox",{name:"Preferred capabilities",exact:true}).fill(capability);
    await page.getByRole("button",{name:"Approve & Continue"}).click();
   }
   runId=(await page.getByRole("link",{name:"View trace"}).getAttribute("href"))!.split("/").at(-1)!;
   const read=async(path:string)=>{const res=await request.get(`${api}${path}`);expect(res.ok()).toBe(true);return res.json();};
   await expect.poll(async()=> (await read(`/api/runs/${runId}/projection`)).tool_progress.some((p:{phase:string})=>p.phase==="executing")).toBe(true);
   const initial=await read(`/api/runs/${runId}`);
   const conv=initial.conversation_id;
   await page.getByPlaceholder("Ask AgentFlow anything...").fill("STEERING_CANARY: use concise wording");
   await page.getByRole("button",{name:"Steer current run",exact:true}).click();
   await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
   await page.getByPlaceholder("Ask AgentFlow anything...").fill("Next task: confirm the retained receipt");
   await page.getByRole("button",{name:"Queue follow-up",exact:true}).click();
   await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
   const receipts=await read(`/api/conversations/${conv}/inputs`);
   expect(receipts).toHaveLength(2);expect(receipts.every((i:{status:string})=>i.status==="queued")).toBe(true);
   const key=`duplicate-${mode}`;
   const body={kind:"follow_up",run_id:runId,content:"Withdraw this task",idempotency_key:key};
   const extra=await (await request.post(`${api}/api/conversations/${conv}/inputs`,{data:body})).json();
   expect((await (await request.post(`${api}/api/conversations/${conv}/inputs`,{data:body})).json()).id).toBe(extra.id);
   expect((await request.post(`${api}/api/conversations/${conv}/inputs`,{data:{...body,content:"Conflict"}})).status()).toBe(409);
   expect((await request.delete(`${api}/api/conversations/${conv}/inputs/${extra.id}`)).status()).toBe(200);
   await page.reload();
   await page.locator(".run-inbox-receipts summary").click();
   await expect(page.locator(".run-inbox-receipts")).toContainText("STEERING_CANARY");
   expect((await request.post(`${api}/__fixture/tool-progress/release`)).status()).toBe(204);
   await expect.poll(async()=> (await read(`/api/runs/${runId}`)).status).toBe("completed");
   await expect.poll(async()=> (await read(`/api/conversations/${conv}/inputs`)).filter((i:{status:string})=>i.status==="applied").length).toBe(2);
   const final=await read(`/api/conversations/${conv}/inputs`);
   const followup=final.find((i:{kind:string;status:string})=>i.kind==="follow_up"&&i.status==="applied");
   followupRunId=followup.applied_run_id;
   expect(followup.applied_run_id).not.toBe(runId);
   // Receipt application is not execution completion. Do not let a subsequent
   // case re-block this case's still-running sandbox worker.
   await expect.poll(async()=> (await read(`/api/runs/${followupRunId}`)).status).toBe(mode==="Multi-agent"?"waiting_for_user":"completed");
   const replay=await read(`/api/runs/${runId}/replay`);
   const steer=final.find((i:{kind:string})=>i.kind==="steer");
   expect(steer.turn_id).toBeTruthy();expect(!!steer.stage_id).toBe(mode!=="Single agent");
   expect(replay.projection.invariant_failures??[]).toEqual([]);
   const manifests=replay.run_events.filter((e:{type:string})=>e.type==="context.assembled");
   expect(JSON.stringify(manifests)).toContain(steer.id);
   if(mode==="Multi-agent") expect(replay.steps.find((s:{id:string})=>s.id===steer.stage_id)?.role).not.toBe("worker");
   const messages=await read(`/api/conversations/${conv}/messages`);
   expect(messages.filter((m:{id:string})=>m.id===steer.id)).toHaveLength(1);
   expect(messages.filter((m:{id:string})=>m.id===followup.id)).toHaveLength(1);
   const contracts=await read("/__fixture/contracts");expect((contracts.failures??[]).slice(before)).toEqual([]);
   await test.info().attach("durable-input-evidence.json",{body:JSON.stringify({schema:"durable-input-evidence-v1",mode,input:"sandbox-gate progress-wait inbox-steer",run:replay.run,snapshot:replay.runtime_snapshot,receipts:final,manifests,checks:["browser to Go to Postgres","complete batch before steering","idempotency conflict","withdrawal","reload restore","new follow-up Run","no isolated Worker broadcast"],limitations:["deterministic provider and sandbox fixtures, not live model compliance","restart recovery covered separately by Postgres integration"]},null,2),contentType:"application/json"});
  } finally {
   await request.post(`${api}/__fixture/tool-progress/release`);
   if(runId)await request.post(`${api}/api/runs/${runId}/cancel`);
   if(followupRunId)await request.post(`${api}/api/runs/${followupRunId}/cancel`);
  }
 });
}

for(const outcome of ["steer","cancel"]) {
 test(`final-answer boundary: ${outcome} preserves queued input and ordinary Send`,async({page,request})=>{
  await page.goto("/workspace");
  await expect(page.getByText("API connected",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"New conversation",exact:true}).click();
  await page.getByRole("button",{name:"Direct Single agent",exact:true}).click();
  await page.getByPlaceholder("Ask AgentFlow anything...").fill("stream-gate: wait for another instruction");
  await page.getByRole("button",{name:"Send message",exact:true}).click();
  await expect(page.locator(".message.assistant").last()).toContainText("First token");
  const id=(await page.getByRole("link",{name:"View trace"}).getAttribute("href"))!.split("/").at(-1)!;
  const read=async(path:string)=>{const res=await request.get(`${api}${path}`);expect(res.ok()).toBe(true);return res.json();};
  const run=await read(`/api/runs/${id}`);const path=`/api/conversations/${run.conversation_id}/inputs`;
  try {
   await page.getByPlaceholder("Ask AgentFlow anything...").fill(outcome==="steer"?"STEERING_CANARY: revise the final answer":"Next independent task");
   await page.getByRole("button",{name:outcome==="steer"?"Steer current run":"Queue follow-up",exact:true}).click();
   await expect(page.getByPlaceholder("Ask AgentFlow anything...")).toHaveValue("");
   const receipt=(await read(path))[0];
   if(outcome==="steer"){
    expect((await request.post(`${api}/__fixture/release`)).status()).toBe(204);
    await expect(page.getByLabel("Task status: completed",{exact:true})).toBeVisible();
    await expect(page.locator(".message.assistant").last()).toContainText("Evidence saved.");
    await expect(page.locator(".message.assistant").last()).not.toContainText("First token");
    expect((await read(path))[0].status).toBe("applied");
   }else{
    await page.locator(".topbar").getByRole("button",{name:"Stop",exact:true}).click();
    await expect(page.getByLabel("Task status: canceled",{exact:true})).toBeVisible();
    expect((await read(path))[0].status).toBe("queued");
    await page.reload();
    await page.locator(".run-inbox-receipts summary").click();
    await page.getByRole("button",{name:"Start next follow-up",exact:true}).click();
    await expect.poll(async()=> (await read(path))[0].status).toBe("applied");
    const applied=(await read(path))[0];
    await expect.poll(async()=> (await read(`/api/runs/${applied.applied_run_id}`)).status).toBe("completed");
    expect((await read(`/api/runs/${id}`)).status).toBe("canceled");
   }
   await test.info().attach("final-boundary-evidence.json",{body:JSON.stringify({schema:"durable-input-final-boundary-v1",outcome,run:await read(`/api/runs/${id}`),receipt:await read(path),input:receipt.content,checks:["explicit input action","original Run status preserved","durable receipt"],limitations:["controlled provider, not model quality"]},null,2),contentType:"application/json"});
  }finally{await request.post(`${api}/__fixture/release`);await request.post(`${api}/api/runs/${id}/cancel`);}
 });
}
