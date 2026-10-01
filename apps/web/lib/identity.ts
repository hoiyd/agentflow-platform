import type { components } from "./api-contract.gen";
import { apiObject, apiVoid } from "./api-client";

export type IdentitySession = components["schemas"]["IdentitySession"];

export function getIdentitySession(signal?: AbortSignal): Promise<IdentitySession> {
  return apiObject<IdentitySession>("/api/auth/session", { signal, cache: "no-store" }, { errorMessage: "Failed to check session" }, "identity session");
}

export function signOut(): Promise<void> {
  return apiVoid("/api/auth/logout", { method: "POST" }, { errorMessage: "Failed to sign out" });
}
