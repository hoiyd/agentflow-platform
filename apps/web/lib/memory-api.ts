import { apiArray, apiObject } from "./api-client.ts";

export type MemoryInfo = {
  id: string;
  version: number;
  deleted_at?: string;
  workspace_id?: string;
  user_id?: string;
  project_id?: string;
  conversation_id?: string;
  run_id?: string;
  source_message_id?: string;
  kind: string;
  content: string;
  metadata?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

export type RetrievedMemory = {
  memory: MemoryInfo;
  similarity: number;
  recency_boost: number;
  score: number;
};

export type CreateMemoryInput = {
  kind: string;
  content: string;
  metadata?: Record<string, unknown>;
};

export type SearchMemoriesInput = {
  query: string;
  limit?: number;
  metadata?: Record<string, string>;
};

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

export type MemoryMutation = {
  operation_id: string;
  expected_version: number;
  action: "replace" | "delete";
  content?: string;
  actor: string;
  reason: string;
};

export type MemoryChange = {
  workspace_id: string;
  memory_id: string;
  operation_id: string;
  action: "replace" | "delete";
  previous_version: number;
  version: number;
  source_message_id?: string;
  actor: string;
  reason: string;
  created_at: string;
};

export type MemoryDetail = { memory: MemoryInfo; changes: MemoryChange[] };
export type MemoryMutationResult = { memory: MemoryInfo; change: MemoryChange; applied: boolean };

export function getMemory(id: string): Promise<MemoryDetail> {
  return apiObject<MemoryDetail>(`/api/memories/${encodeURIComponent(id)}`, { cache: "no-store" },
    { errorMessage: "Failed to load memory", includeErrorBody: true }, "memory detail");
}

export function mutateMemory(id: string, command: MemoryMutation): Promise<MemoryMutationResult> {
  return apiObject<MemoryMutationResult>(`/api/memories/${encodeURIComponent(id)}/mutations`, {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(command)
  }, { errorMessage: "Failed to update memory", includeErrorBody: true }, "memory mutation");
}
