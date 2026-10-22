import type { ToolInfo } from "./api";

const labels: Record<string, string> = {
  service_disabled: "Disabled by operator",
  workspace_disabled: "Disabled in this Workspace",
  policy_denied: "Denied by execution policy",
  sandbox_disabled: "Sandbox disabled by operator",
  credential_unavailable: "Credential unavailable",
  filesystem_unconfigured: "Filesystem roots not configured",
  filesystem_write_unconfigured: "Managed output roots not configured",
  filesystem_root_changed: "Filesystem roots changed; operator review required",
  mcp_discovery_pending: "MCP discovery in progress",
  mcp_connection_failed: "MCP server unavailable",
  mcp_authentication_failed: "MCP authentication rejected",
  mcp_discovery_failed: "MCP discovery failed",
  mcp_schema_unsupported: "MCP schema unsupported",
  mcp_tool_missing: "Tool no longer offered by MCP server",
  mcp_definition_changed: "MCP definition changed; start a new Run",
  mcp_adapter_closed: "MCP adapter stopped"
};

export function toolAvailabilityLabel(tool: Pick<ToolInfo, "excluded_reason" | "unavailable_reason">) {
  const reason = tool.excluded_reason || tool.unavailable_reason;
  return reason ? labels[reason] ?? "Unavailable" : "";
}
