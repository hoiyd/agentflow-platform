import type { components } from "./api-contract.gen";
import { apiJSON, apiObject, apiVoid, isObject } from "./api-client";

export type Workspace = components["schemas"]["Workspace"];
export type WorkspaceUpdate = components["schemas"]["WorkspaceUpdateRequest"];

export async function listWorkspaces(signal?: AbortSignal): Promise<Workspace[]> {
  const value = await apiJSON("/api/workspaces", { signal, cache: "no-store" }, { errorMessage: "Failed to load Workspaces" });
  if (!Array.isArray(value) || value.some(item => !isObject(item) || typeof item.id !== "string" || !/^[1-9]\d*$/.test(item.id) || typeof item.name !== "string" || !["active", "archived"].includes(String(item.status)))) {
    throw new Error("Invalid Workspace list response");
  }
  return value as Workspace[];
}
export function createWorkspace(input: components["schemas"]["WorkspaceCreateRequest"]): Promise<Workspace> {
  return apiObject<Workspace>("/api/workspaces", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input) }, { errorMessage: "Failed to create Workspace" }, "Workspace");
}
export function updateWorkspace(id: string, input: WorkspaceUpdate): Promise<Workspace> {
  return apiObject<Workspace>(`/api/workspaces/${encodeURIComponent(id)}`, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input) }, { errorMessage: "Failed to update Workspace" }, "Workspace");
}
export function deleteWorkspace(id: string, replacement: string): Promise<void> {
  const query = replacement ? `?replacement_workspace_id=${encodeURIComponent(replacement)}` : "";
  return apiVoid(`/api/workspaces/${encodeURIComponent(id)}${query}`, { method: "DELETE" }, { errorMessage: "Failed to delete Workspace" });
}
