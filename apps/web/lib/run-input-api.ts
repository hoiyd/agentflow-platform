import type { components } from "./api-contract.gen";
import { apiArray, apiObject } from "./api-client";

export type RunInput = components["schemas"]["RunInput"];
export type RunInputRequest = components["schemas"]["RunInputRequest"];
const path = (id: string) => `/api/conversations/${encodeURIComponent(id)}/inputs`;
export function listRunInputs(id: string, signal?: AbortSignal): Promise<RunInput[]> {
 return apiArray<RunInput>(path(id), {cache:"no-store",signal}, {errorMessage:"Failed to load queued inputs"});
}
export function enqueueRunInput(id: string, input: RunInputRequest): Promise<RunInput> {
 return apiObject<RunInput>(path(id), {method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(input)}, {errorMessage:"Failed to queue input",includeErrorBody:true}, "input receipt");
}
export function withdrawRunInput(id: string, inputId: string): Promise<RunInput> {
 return apiObject<RunInput>(`${path(id)}/${encodeURIComponent(inputId)}`, {method:"DELETE"}, {errorMessage:"Failed to withdraw input",includeErrorBody:true}, "input receipt");
}
export function startFollowup(id: string): Promise<{status:string}> {
 return apiObject<{status:string}>(`${path(id)}/start`, {method:"POST"}, {errorMessage:"Failed to start follow-up",includeErrorBody:true}, "dispatch receipt");
}
