import { apiArray, apiObject } from "./api-client.ts";
import type { components } from "./api-contract.gen.ts";

type MemorySchemas = components["schemas"];

export type MemoryInfo = MemorySchemas["Memory"];
export type RetrievedMemory = MemorySchemas["RetrievedMemory"];
export type CreateMemoryInput = MemorySchemas["CreateMemoryRequest"];
export type SearchMemoriesInput = MemorySchemas["MemorySearchRequest"];

export async function createMemory(input: CreateMemoryInput): Promise<MemoryInfo> {
  return apiObject<MemoryInfo>(
    "/api/memories",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Failed to save memory" },
    "memory"
  );
}

export async function searchMemories(input: SearchMemoriesInput): Promise<RetrievedMemory[]> {
  return apiArray<RetrievedMemory>(
    "/api/memories/search",
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input)
    },
    { errorMessage: "Failed to search memories" }
  );
}

export type MemoryMutation = MemorySchemas["MemoryMutation"];
export type MemoryChange = MemorySchemas["MemoryChange"];
export type MemoryDetail = Omit<MemorySchemas["MemoryDetail"], "changes"> & { changes: MemoryChange[] };
export type MemoryMutationResult = MemorySchemas["MemoryMutationResult"];

export async function getMemory(id: string): Promise<MemoryDetail> {
  const detail = await apiObject<MemorySchemas["MemoryDetail"]>(`/api/memories/${encodeURIComponent(id)}`, { cache: "no-store" },
    { errorMessage: "Failed to load memory", includeErrorBody: true }, "memory detail");
  return { ...detail, changes: Array.isArray(detail.changes) ? detail.changes : [] };
}

export function mutateMemory(id: string, command: MemoryMutation): Promise<MemoryMutationResult> {
  return apiObject<MemoryMutationResult>(`/api/memories/${encodeURIComponent(id)}/mutations`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(command)
  }, { errorMessage: "Failed to update memory", includeErrorBody: true }, "memory mutation");
}
