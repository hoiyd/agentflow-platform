import { useEffect, useState } from "react";
import { getWorkspaceID } from "../../lib/api-client";
import type { components } from "../../lib/api-contract.gen";

export type ScopeValues = { product: string; environment: string; language: string };
export const EMPTY_SCOPE: ScopeValues = { product: "", environment: "", language: "" };
export const environments = ["development", "test", "staging", "production"];
export type KnowledgeFilter = components["schemas"]["MetadataFilter"];

export function scopeFilter(values: ScopeValues): KnowledgeFilter | undefined {
  const all = (["product", "environment", "language"] as const).filter(key => values[key].trim()).map(key => ({
    field: `document.metadata.${key}` as "document.metadata.product" | "document.metadata.environment" | "document.metadata.language",
    op: "eq" as const, value: values[key]
  }));
  return all.length ? { schema_version: "knowledge-metadata-v1", where: { all } } : undefined;
}

export function useKnowledgeScope() {
  const [key] = useState(() => `agentflow-knowledge-scope:${getWorkspaceID()}`);
  const [values, setValues] = useState<ScopeValues>(() => {
    try {
      const stored = JSON.parse(sessionStorage.getItem(key) ?? "null");
      if (stored && typeof stored.product === "string" && stored.product.length <= 120 && typeof stored.language === "string" && stored.language.length <= 35 &&
        (stored.environment === "" || environments.includes(stored.environment))) return { product: stored.product, environment: stored.environment, language: stored.language };
    } catch { /* An unavailable or malformed browser cache must not inject a filter. */ }
    return { ...EMPTY_SCOPE };
  });
  useEffect(() => { try { sessionStorage.setItem(key, JSON.stringify(values)); } catch { /* Storage may be unavailable; in-memory settings remain usable. */ } }, [key, values]);
  return { values, change: setValues, filter: scopeFilter(values) };
}
