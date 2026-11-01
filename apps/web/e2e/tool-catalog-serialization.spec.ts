import { test, expect } from "@playwright/test";
import { defaultWorkspaceID } from "./fixtures/workspace-agent";

test("Tool catalog serializes writes, unlocks failure and persists the next action",async({page,request},info)=>{
 const api="http://127.0.0.1:18080";
 await page.goto("/workspace");
 const workspace=await defaultWorkspaceID(page);
 const headers={"X-Workspace-ID":workspace,Origin:"http://127.0.0.1:13000"};
 for(const name of ["get_current_time","calculator"]){expect((await request.post(`${api}/api/tools/${name}/enable`,{headers})).ok()).toBe(true);}
 await page.getByRole("button",{name:"Tools",exact:true}).click();
 const a=page.getByRole("checkbox",{name:"Allow get_current_time in this Workspace",exact:true});
 const b=page.getByRole("checkbox",{name:"Allow calculator in this Workspace",exact:true});
 await expect(a).toBeChecked();await expect(b).toBeChecked();
 let release!:()=>void,arrived!:()=>void;
 const gate=new Promise<void>(resolve=>{release=resolve;});
 const ready=new Promise<void>(resolve=>{arrived=resolve;});
 await page.route("**/api/tools/get_current_time/disable",async route=>{arrived();await gate;await route.continue();});
 try{
  await a.click();await ready;
  await expect(a).toBeDisabled();await expect(b).toBeDisabled();
  await info.attach("tool-catalog-pending-desktop.png",{body:await page.locator(".tools-panel").screenshot(),contentType:"image/png"});
  release();await expect(a).not.toBeChecked();await expect(b).toBeEnabled();
  let rejectOnce=true;
  await page.route("**/api/tools/calculator/disable",async route=>{
   if(rejectOnce){rejectOnce=false;await route.fulfill({status:503,json:{error:"Synthetic catalog write failure"}});}
   else await route.continue();
  });
  await b.click();
  await expect(page.locator(".tools-panel .error")).toContainText("Failed to update tool");
  await expect(a).toBeEnabled();await expect(b).toBeEnabled();await expect(b).toBeChecked();
  await b.click();await expect(b).not.toBeChecked();await expect(b).toBeEnabled();
  const catalog=await(await request.get(`${api}/api/tools`,{headers})).json();
  for(const name of ["get_current_time","calculator"]){expect(catalog.find((tool:{name:string})=>tool.name===name).workspace_enabled).toBe(false);}
  await page.reload();await page.getByRole("button",{name:"Tools",exact:true}).click();
  await expect(a).not.toBeChecked();await expect(b).not.toBeChecked();
  await info.attach("tool-catalog-write-evidence.json",{contentType:"application/json",body:JSON.stringify({workspace_id:workspace,catalog,checks:["all toggles disabled during write","failure preserves prior state and unlocks","next write persists","reload"],limitations:["one intentional 503 injected","successful writes use real Go and Postgres"]},null,2)});
 }finally{
  release();
  for(const name of ["get_current_time","calculator"]){await request.post(`${api}/api/tools/${name}/enable`,{headers});}
 }
});
