import type { components } from "./api-contract.gen";
import { apiArray, apiObject, apiRequest } from "./api-client";
import { readChatEventStream } from "./run-stream";
import type { ChatEvent } from "./api-types";

export type Clarification = components["schemas"]["Clarification"];
export type ClarificationAnswerRequest = components["schemas"]["ClarificationAnswerRequest"];
const path = (run: string) => `/api/runs/${encodeURIComponent(run)}/clarifications`;
export function listClarifications(run: string, signal?: AbortSignal): Promise<Clarification[]> {
 return apiArray(path(run), {cache:"no-store",signal}, {errorMessage:"Failed to load clarification"});
}
export function answerClarification(run: string, id: string, input: ClarificationAnswerRequest, signal?: AbortSignal): Promise<Clarification> {
 return apiObject(`${path(run)}/${encodeURIComponent(id)}/answers`, {method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(input),signal}, {errorMessage:"Failed to save answer",includeErrorBody:true}, "clarification receipt");
}
export async function resumeClarification(run: string,id: string,onEvent:(event:ChatEvent)=>void,signal?:AbortSignal):Promise<void>{
 const response=await apiRequest(`${path(run)}/${encodeURIComponent(id)}/resume`,{method:"POST",signal},{errorMessage:"Failed to continue clarified run",includeErrorBody:true,requireBody:true});
 await readChatEventStream(response,onEvent);
}
