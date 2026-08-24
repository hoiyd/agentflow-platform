const API_BASE = process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";
const configuredWorkspaceId = process.env.NEXT_PUBLIC_WORKSPACE_ID?.trim();
const workspaceId = !configuredWorkspaceId || configuredWorkspaceId === "default"
  ? "default_workspace"
  : configuredWorkspaceId;

type APIRequestPolicy = {
  errorMessage: string;
  includeErrorBody?: boolean;
  requireBody?: boolean;
};

export async function apiRequest(
  path: string,
  init: RequestInit,
  policy: APIRequestPolicy
): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("X-Workspace-ID", workspaceId);
  const response = await fetch(`${API_BASE}${path}`, { ...init, headers });
  if (response.ok && (!policy.requireBody || response.body)) {
    return response;
  }

  const detail = policy.includeErrorBody ? (await response.text()).trim() : "";
  throw new Error(`${policy.errorMessage}: ${response.status}${detail ? ` ${detail}` : ""}`);
}

export async function apiJSON(
  path: string,
  init: RequestInit,
  policy: APIRequestPolicy
): Promise<unknown> {
  const response = await apiRequest(path, init, policy);
  try {
    return await response.json();
  } catch {
    throw new Error(`${policy.errorMessage}: invalid JSON response`);
  }
}

export async function apiObject<T extends object>(
  path: string,
  init: RequestInit,
  policy: APIRequestPolicy,
  responseName: string
): Promise<T> {
  return expectObject<T>(await apiJSON(path, init, policy), responseName);
}

export async function apiArray<T>(
  path: string,
  init: RequestInit,
  policy: APIRequestPolicy
): Promise<T[]> {
  const value = await apiJSON(path, init, policy);
  return Array.isArray(value) ? (value as T[]) : [];
}

export async function apiVoid(
  path: string,
  init: RequestInit,
  policy: APIRequestPolicy
): Promise<void> {
  await apiRequest(path, init, policy);
}

export function expectObject<T extends object>(value: unknown, responseName: string): T {
  if (!isObject(value)) {
    throw new Error(`Invalid ${responseName} response: expected an object`);
  }
  return value as T;
}

export function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
