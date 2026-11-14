import type { components } from "./api-contract.gen";
import { apiArray, apiObject, apiRequest } from "./api-client";
import { readChatEventStream } from "./run-stream";
import type { ChatEvent } from "./api-types";

export type ToolApproval = components["schemas"]["ToolApproval"];
export type ToolApprovalDecision = components["schemas"]["ToolApprovalDecision"];
const path = (run: string) => `/api/runs/${encodeURIComponent(run)}/tool-approvals`;

export function listToolApprovals(run: string, signal?: AbortSignal): Promise<ToolApproval[]> {
  return apiArray(path(run), { cache: "no-store", signal }, { errorMessage: "Failed to load Tool approvals" });
}

export function decideToolApproval(run: string, id: string, input: ToolApprovalDecision, signal?: AbortSignal): Promise<ToolApproval> {
  return apiObject(`${path(run)}/${encodeURIComponent(id)}/decision`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input), signal
  }, { errorMessage: "Failed to save Tool approval", includeErrorBody: true }, "Tool approval receipt");
}

export async function resumeToolApproval(run: string, id: string, onEvent: (event: ChatEvent) => void, signal?: AbortSignal): Promise<void> {
  const response = await apiRequest(`${path(run)}/${encodeURIComponent(id)}/resume`, { method: "POST", signal }, {
    errorMessage: "Failed to resume approved Tool call", includeErrorBody: true, requireBody: true
  });
  await readChatEventStream(response, onEvent);
}
