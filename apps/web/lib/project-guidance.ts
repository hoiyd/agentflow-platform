import type { components } from "./api-contract.gen";
import { apiObject } from "./api-client.ts";

export type ProjectContext = components["schemas"]["ProjectContext"];
export type ProjectCatalog = components["schemas"]["ProjectGuidanceCatalog"];
export type ProjectFilePage = components["schemas"]["ProjectFilePage"];
export type ProjectGuidanceSnapshot = components["schemas"]["ProjectGuidanceSnapshot"];

export async function listGuidanceProjects(signal?: AbortSignal): Promise<ProjectCatalog> {
  const result = await apiObject<ProjectCatalog>("/api/project-guidance/projects", { signal, cache: "no-store" }, { errorMessage: "Project instructions unavailable" }, "project instructions");
  if (typeof result.configured !== "boolean" || !Array.isArray(result.projects)) throw new Error("Invalid project instructions catalog");
  return result;
}

export function listGuidanceFiles(root: string, directory: string, signal?: AbortSignal): Promise<ProjectFilePage> {
  return apiObject<ProjectFilePage>(`/api/project-guidance/projects/${encodeURIComponent(root)}/files?${new URLSearchParams({ path: directory })}`,
    { signal, cache: "no-store" }, { errorMessage: "Task files unavailable" }, "task files");
}

export function savedProjectContext(value: unknown): ProjectContext | undefined {
  if (!value || typeof value !== "object" || !("root" in value) || !("paths" in value)) return;
  if (typeof value.root !== "string" || !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$/.test(value.root) || !Array.isArray(value.paths) || !value.paths.length || value.paths.length > 16) return;
  if (!value.paths.every((name): name is string => typeof name === "string" && name.length <= 1024 && !/[\\\x00\r\n*?\[\]]/.test(name) && name.split("/").every(part => part !== "" && part !== "." && part !== ".."))) return;
  return { root: value.root, paths: [...new Set(value.paths)].sort() };
}
