import type { components } from "./api-contract.gen";
import { apiArray, apiObject, apiVoid } from "./api-client";

export type MCPConnection = components["schemas"]["MCPConnection"];
export type MCPConnectionInput = components["schemas"]["MCPConnectionRequest"];

const base = "/api/mcp/connections";
export function listMCPConnections(signal?: AbortSignal): Promise<MCPConnection[]> {
  return apiArray<MCPConnection>(base, { signal, cache: "no-store" }, { errorMessage: "Failed to load MCP connections" });
}
export function saveMCPConnection(id: string, input: MCPConnectionInput): Promise<MCPConnection> {
  return apiObject<MCPConnection>(id ? `${base}/${encodeURIComponent(id)}` : base,
    { method: id ? "PATCH" : "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input) },
    { errorMessage: "Failed to save MCP connection" }, "MCP connection");
}
export function testMCPConnection(id: string): Promise<MCPConnection> {
  return apiObject<MCPConnection>(`${base}/${encodeURIComponent(id)}/test`, { method: "POST" },
    { errorMessage: "Failed to test MCP connection" }, "MCP connection");
}
export function deleteMCPConnection(id: string): Promise<void> {
  return apiVoid(`${base}/${encodeURIComponent(id)}`, { method: "DELETE" }, { errorMessage: "Failed to delete MCP connection" });
}

export function connectionStatus(connection: MCPConnection): string {
  if (connection.status === "discovered") return connection.enabled ? "Enabled" : "Disabled";
  const labels: Record<string, string> = {
    configured: "Not tested", credential_unavailable: "Credential unavailable",
    mcp_authentication_failed: "Authentication failed", mcp_connection_failed: "Connection failed",
    mcp_discovery_failed: "Discovery failed"
  };
  return labels[connection.status] ?? connection.status.replaceAll("_", " ");
}
