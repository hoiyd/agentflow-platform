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

type APIErrorOptions = {
  status: number;
  code?: string;
  source?: string;
  category?: string;
  retryable?: boolean;
  operation?: string;
  requestId?: string;
};

type APIErrorEnvelope = {
  error?: string;
  code?: string;
  source?: string;
  category?: string;
  retryable?: boolean;
  operation?: string;
  request_id?: string;
};

export class APIError extends Error {
  readonly status: number;
  readonly code: string;
  readonly source: string;
  readonly category: string;
  readonly retryable: boolean;
  readonly operation?: string;
  readonly requestId?: string;

  constructor(message: string, options: APIErrorOptions) {
    super(message);
    this.name = "APIError";
    this.status = options.status;
    this.code = options.code ?? "request_failed";
    this.source = options.source ?? "http_api";
    this.category = options.category ?? "execution";
    this.retryable = options.retryable ?? false;
    this.operation = options.operation;
    this.requestId = options.requestId;
  }
}

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

  const body = await response.text();
  const envelope = parseErrorEnvelope(body);
  const detail = policy.includeErrorBody ? envelope.error ?? body.trim() : "";
  throw new APIError(
    `${policy.errorMessage}: ${response.status}${detail ? ` ${detail}` : ""}`,
    {
      status: response.status,
      code: envelope.code,
      source: envelope.source,
      category: envelope.category,
      retryable: envelope.retryable,
      operation: envelope.operation,
      requestId: envelope.request_id ?? response.headers.get("X-Request-ID") ?? undefined
    }
  );
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

function parseErrorEnvelope(body: string): APIErrorEnvelope {
  try {
    const value = JSON.parse(body) as unknown;
    if (!isObject(value)) return {};
    return {
      error: stringValue(value.error),
      code: stringValue(value.code),
      source: stringValue(value.source),
      category: stringValue(value.category),
      retryable: typeof value.retryable === "boolean" ? value.retryable : undefined,
      operation: stringValue(value.operation),
      request_id: stringValue(value.request_id)
    };
  } catch {
    return {};
  }
}

function stringValue(value: unknown): string | undefined {
  return typeof value === "string" && value !== "" ? value : undefined;
}
