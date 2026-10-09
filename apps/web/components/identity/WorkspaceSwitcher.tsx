"use client";

import type { Workspace } from "../../lib/workspaces";
import { SelectionMenu } from "../ui/SelectionMenu";

export function WorkspaceSwitcher({ workspaces, selected, onChange }: {
  workspaces: Workspace[];
  selected: string;
  onChange: (workspace: string) => void;
}) {
  return (
    <SelectionMenu label="Workspace" className="workspace-switcher" value={selected} onChange={onChange}
      options={workspaces.map(item => ({value: item.id, label: item.name, annotation: item.status === "archived" ? "Archived" : undefined}))} />
  );
}
