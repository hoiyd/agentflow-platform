import { useState } from "react";
import { FolderOpen } from "lucide-react";
import { createPortal } from "react-dom";
import type { ProjectContext } from "../../lib/project-guidance";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";
import { ProjectGuidanceDialog } from "./ProjectGuidanceDialog";

export function ProjectGuidanceControl({ value, onChange, disabled }: {
  value?: ProjectContext; onChange: (next?: ProjectContext) => void; disabled: boolean;
}) {
  const [open, setOpen] = useState(false);
  const readOnly = useWorkspaceReadOnly();
  if (open && (disabled || readOnly)) setOpen(false);
  const count = value?.paths.length ?? 0;
  const label = value ? `Project instructions: ${value.root}, ${count} ${count === 1 ? "file" : "files"}` : "Project instructions";
  return <>
    <button type="button" className={`project-guidance-trigger ${value ? "active" : ""}`} aria-label={label}
      aria-haspopup="dialog" aria-expanded={open && !disabled && !readOnly} disabled={disabled || readOnly} title={label} onClick={() => setOpen(true)}>
      <FolderOpen aria-hidden="true" size={16} /><span>{value?.root ?? "Project instructions"}</span>{value ? <strong>{count}</strong> : null}
    </button>
    {open && !disabled && !readOnly ? createPortal(<ProjectGuidanceDialog value={value} onClose={() => setOpen(false)}
      onApply={next => { onChange(next); setOpen(false); }} />, document.body) : null}
  </>;
}
