import { apiArray, apiObject } from "./api-client.ts";

export type MemoryInfo = {
  id: string;
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
