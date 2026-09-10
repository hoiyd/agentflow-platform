import type { DocumentInfo } from "../../lib/knowledge-api";

export function metadataString(metadata: Record<string, unknown> | undefined, key: string) {
  const value = metadata?.[key];
  return typeof value === "string" ? value : "";
}

export function documentFormat(document: DocumentInfo) {
  return metadataString(document.metadata, "format") || document.source_type || "text";
}

export function documentFilename(document: DocumentInfo) {
  return metadataString(document.metadata, "filename") || document.source_uri || "";
}

export function shortSourceLabel(label: string, value: string | undefined) {
  if (!value) return "";
  const normalized = value.startsWith("sha256:") ? value.slice(7) : value;
  return `${label} ${normalized.slice(0, 12)}`;
}
