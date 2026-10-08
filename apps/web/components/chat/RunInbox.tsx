import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { CornerDownRight, ListPlus, Play, X } from "lucide-react";
import { enqueueRunInput, listRunInputs, startFollowup, withdrawRunInput, type RunInput, type RunInputRequest } from "../../lib/run-input-api";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";
import type { ChatMode } from "../../lib/api";

type Props = {
 conversationId:string; runId:string; mode:ChatMode; agentId:string; input:string;
 busy:boolean; canStartFollowup:boolean; onSubmitted:()=>void; onRunAvailable:()=>void;
};

export function RunInbox(props:Props) {
 return <RunInboxContent key={props.conversationId} {...props}/>;
}

function RunInboxContent(props:Props) {
 const readOnly=useWorkspaceReadOnly();
 const [items,setItems]=useState<RunInput[]>([]);
 const [error,setError]=useState("");
 const [pending,setPending]=useState(false);
 const latest=useRef(props);
 const submitting=useRef(false);
 const retry=useRef<{body:string;key:string}|null>(null);
 const notified=useRef(new Set<string>());
 const initialized=useRef(false);
 const mounted=useRef(true);
 useLayoutEffect(()=>{latest.current=props;});

 useEffect(()=>{
  const controller=new AbortController(); let timer:ReturnType<typeof setTimeout>;
  mounted.current=true;
  async function refresh() {
   try {
    const loaded=await listRunInputs(props.conversationId,controller.signal);
    if(controller.signal.aborted)return;
    setItems(loaded);setError("");
    const current=latest.current;
    const applied=loaded.filter(item=>item.kind==="follow_up" && item.status==="applied");
    if(!initialized.current){
     for(const item of applied){if(item.run_id!==current.runId)notified.current.add(item.id);}
     initialized.current=true;
    }
    if(!current.busy && applied.some(item=>!notified.current.has(item.id))){
     for(const item of applied)notified.current.add(item.id);
     current.onRunAvailable();
    }
   } catch(err) {if(!controller.signal.aborted)setError(err instanceof Error?err.message:"Failed to load queued inputs");}
   finally {if(!controller.signal.aborted)timer=setTimeout(refresh,1500);}
  }
  void refresh();
  return ()=>{mounted.current=false;controller.abort();clearTimeout(timer);};
 },[props.conversationId]);

 async function act(action:()=>Promise<unknown>,submittedDraft?:string) {
  if(submitting.current)return;
  submitting.current=true;setPending(true);setError("");
  const conversation=props.conversationId;
  try {
   await action();
   if(!mounted.current || latest.current.conversationId!==conversation)return;
   if(submittedDraft!==undefined) {
    retry.current=null;
    if(latest.current.input===submittedDraft)latest.current.onSubmitted();
   }
   const loaded=await listRunInputs(conversation);
   if(mounted.current && latest.current.conversationId===conversation)setItems(loaded);
  } catch(err) {if(mounted.current && latest.current.conversationId===conversation)setError(err instanceof Error?err.message:"Input action failed");}
  finally {if(mounted.current && latest.current.conversationId===conversation){submitting.current=false;setPending(false);}}
 }

 function submit(kind:RunInputRequest["kind"]) {
  const input={kind,run_id:props.runId,content:props.input.trim(),mode:props.mode,agent_id:props.agentId};
  const body=JSON.stringify(input);
  if(retry.current?.body!==body)retry.current={body,key:crypto.randomUUID()};
  const idempotency_key=retry.current.key;
  void act(()=>enqueueRunInput(props.conversationId,{...input,idempotency_key}),props.input);
 }
 const queued=items.filter(item=>item.status==="queued");
 return <div className="run-inbox" aria-label="Run inbox">
  {props.busy && !readOnly ? <div className="run-inbox-actions">
   <button type="button" aria-label="Steer current run" title="Add a constraint at the next safe boundary" disabled={pending || !props.input.trim()} onClick={()=>submit("steer")}><CornerDownRight size={14}/>Steer current run</button>
   <button type="button" disabled={pending || !props.input.trim()} onClick={()=>submit("follow_up")}><ListPlus size={14}/>Queue follow-up</button>
  </div>:null}
  {error?<div className="error" role="alert">{error}</div>:null}
  {items.length?<details className="run-inbox-receipts"><summary>Input queue{queued.length?` (${queued.length} pending)`:""}</summary>
   <ul>{items.map(item=><li key={item.id}>
    <span>{item.kind==="steer"?"Steer":"Follow-up"}</span>
    <span className="run-inbox-content" title={item.content}>{item.content}</span>
    <span>{item.status==="queued" && item.kind==="steer" && !props.busy?"Not applied: run stopped":item.status}</span>
    {item.status==="applied" && item.kind==="follow_up"?<a href={`/runs/${encodeURIComponent(item.applied_run_id)}`}>View run</a>:null}
    {item.status==="queued" && !readOnly?<button type="button" aria-label={`Withdraw ${item.kind==="steer"?"steer":"follow-up"}: ${item.content}`} title="Withdraw queued input" disabled={pending} onClick={()=>void act(()=>withdrawRunInput(props.conversationId,item.id))}><X size={14}/></button>:null}
   </li>)}</ul>
   {!props.busy && props.canStartFollowup && !readOnly && queued.some(item=>item.kind==="follow_up")?<button type="button" disabled={pending} onClick={()=>void act(()=>startFollowup(props.conversationId))}><Play size={14}/>Start next follow-up</button>:null}
  </details>:null}
 </div>;
}
