import { useEffect, useRef, useState } from "react";
import { listTools, setToolEnabled, type ToolInfo } from "../../lib/api";
import { createLatestRequestController } from "../../lib/latest-request";

export function useToolCatalog() {
  const [tools, setTools] = useState<ToolInfo[]>([]);
  const [error, setError] = useState("");
  const [updatingTool, setUpdatingTool] = useState("");
  const writing = useRef(false);
  const [requests] = useState(createLatestRequestController);

  useEffect(() => () => requests.cancel(), [requests]);

  async function refresh() {
    if (writing.current) return;
    const request = requests.begin();
    setError("");
    try {
      const items = await listTools();
      if (request.isCurrent()) setTools(items);
    } catch (err) {
      if (request.isCurrent()) setError(err instanceof Error ? err.message : "Failed to load tools");
    }
  }

  async function toggle(tool: ToolInfo) {
    if (writing.current) return;
    writing.current = true;
    const request = requests.begin();
    setUpdatingTool(tool.name);
    setError("");
    try {
      const items = await setToolEnabled(tool.name, !(tool.workspace_enabled ?? tool.enabled));
      if (request.isCurrent()) setTools(items);
    } catch (err) {
      if (request.isCurrent()) setError(err instanceof Error ? err.message : "Failed to update tool");
    } finally {
      writing.current = false;
      if (request.isCurrent()) setUpdatingTool("");
    }
  }

  return { tools, error, updatingTool, refresh, toggle };
}
