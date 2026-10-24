import { useState } from "react";
import { getWorkspaceID } from "../../lib/api-client";
import { savedProjectContext, type ProjectContext } from "../../lib/project-guidance";

export function useProjectContext() {
  // Workspace changes remount the authenticated workbench. Persist only the
  // visible new-Run selection, never instruction contents or old Run authority.
  const [key] = useState(() => `agentflow-project-context:${getWorkspaceID()}`);
  const [value, setValue] = useState<ProjectContext | undefined>(() => {
    try { return savedProjectContext(JSON.parse(sessionStorage.getItem(key) ?? "null")); }
    catch { return undefined; }
  });
  function change(next?: ProjectContext) {
    setValue(next);
    try {
      if (next) sessionStorage.setItem(key, JSON.stringify(next));
      else sessionStorage.removeItem(key);
    } catch { /* The in-memory selection still works when browser storage is unavailable. */ }
  }
  return { value, change };
}
