import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from "@testing-library/react";
import { useEffect } from "react";
import { afterEach, expect, it, vi } from "vitest";
import { ToolsPanel } from "./ChatChrome";
import { useToolCatalog } from "./useToolCatalog";

const api=vi.hoisted(()=>({listTools:vi.fn(),setToolEnabled:vi.fn()}));
vi.mock("../../lib/api",async original=>({...await original<typeof import("../../lib/api")>(),...api}));
afterEach(()=>{cleanup();vi.resetAllMocks();});
const tools=["a","b"].map(name=>({name,description:name,parameters:{},enabled:true,service_enabled:true,workspace_enabled:true}));

// Failure inventory: a pending write must block every toggle, including retained
// callbacks before render; failure unlocks with an error; retry updates the view.
it("serializes retained handlers synchronously without a refresh stealing ownership",async()=>{
 let finish!:(value:unknown)=>void;
 api.listTools.mockResolvedValue(tools);
 api.setToolEnabled.mockImplementation(()=>new Promise(resolve=>{finish=resolve;}));
 const {result}=renderHook(useToolCatalog);
 const toggle=result.current.toggle;
 let pending!:Promise<void>;
 act(()=>{pending=toggle(tools[0]);void toggle(tools[1]);void result.current.refresh();});
 expect(api.setToolEnabled).toHaveBeenCalledTimes(1);
 expect(api.listTools).not.toHaveBeenCalled();
 await act(async()=>{finish(tools);await pending;});
 expect(result.current.updatingTool).toBe("");
});

it("disables the whole catalog during failure and permits the next Tool after settlement",async()=>{
 let fail!:(error:Error)=>void;
 api.listTools.mockResolvedValue(tools);
 api.setToolEnabled.mockImplementationOnce(()=>new Promise((_resolve,reject)=>{fail=reject;}))
  .mockResolvedValueOnce(tools.map(tool=>({...tool,enabled:tool.name!=="b",workspace_enabled:tool.name!=="b"})));
 function Fixture(){
  const catalog=useToolCatalog();
  useEffect(()=>{void catalog.refresh();},[]); // eslint-disable-line react-hooks/exhaustive-deps
  return <ToolsPanel {...catalog} onToggle={tool=>void catalog.toggle(tool)} onCatalogChanged={()=>void catalog.refresh()}/>;
 }
 render(<Fixture/>);
 await screen.findByRole("checkbox",{name:"Allow a in this Workspace"});
 fireEvent.click(screen.getByRole("checkbox",{name:"Allow a in this Workspace"}));
 const b=screen.getByRole("checkbox",{name:"Allow b in this Workspace"});
 expect(b).toHaveProperty("disabled",true);
 await act(async()=>fail(new Error("write rejected")));
 await screen.findByText("write rejected");
 expect(b).toHaveProperty("disabled",false);
 fireEvent.click(b);
 await waitFor(()=>expect(b).toHaveProperty("checked",false));
 expect(api.setToolEnabled).toHaveBeenLastCalledWith("b",false);
});
