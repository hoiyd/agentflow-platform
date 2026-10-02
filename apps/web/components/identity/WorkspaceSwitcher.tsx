"use client";

import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { Check, ChevronDown } from "lucide-react";
import type { Workspace } from "../../lib/workspaces";

export function WorkspaceSwitcher({ workspaces, selected, onChange }: {
  workspaces: Workspace[];
  selected: string;
  onChange: (workspace: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const options = useRef<(HTMLButtonElement | null)[]>([]);
  const listID = useId();
  const label = (id: string) => workspaces.find(item => item.id === id)?.name ?? "Select workspace";

  useEffect(() => {
    if (!open) return;
    options.current[Math.max(0, workspaces.findIndex(item => item.id === selected))]?.focus();
    const dismiss = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open, selected, workspaces]);

  function navigate(event: KeyboardEvent<HTMLButtonElement>, index: number) {
    let next: number;
    switch (event.key) {
      case "ArrowDown": next = (index + 1) % workspaces.length; break;
      case "ArrowUp": next = (index - 1 + workspaces.length) % workspaces.length; break;
      case "Home": next = 0; break;
      case "End": next = workspaces.length - 1; break;
      default: return;
    }
    event.preventDefault();
    options.current[next]?.focus();
  }

  return (
    <div className="workspace-switcher" ref={root}
      onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}
      onKeyDown={event => {
        if (event.key === "Escape" && open) {
          event.preventDefault();
          setOpen(false);
          trigger.current?.focus();
        }
      }}>
      <span className="workspace-switcher-label">Workspace</span>
      <button className="workspace-switcher-trigger" ref={trigger} type="button"
        aria-label={`Workspace: ${label(selected)}`} aria-haspopup="listbox"
        aria-expanded={open} aria-controls={open ? listID : undefined}
        onClick={() => setOpen(value => !value)}
        onKeyDown={event => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            setOpen(true);
          }
        }}>
        <span>{label(selected)}</span><ChevronDown size={14} aria-hidden="true" />
      </button>
      {open ? <div className="workspace-switcher-menu" id={listID} role="listbox" aria-label="Workspace">
        {workspaces.map((item, index) => <button className="workspace-switcher-option" key={item.id}
          ref={element => { options.current[index] = element; }} type="button" role="option"
          aria-selected={item.id === selected} tabIndex={-1} onKeyDown={event => navigate(event, index)}
          onClick={() => {
            setOpen(false);
            trigger.current?.focus();
            if (item.id !== selected) onChange(item.id);
          }}>
          <span>{item.name}{item.status === "archived" ? <small>Archived</small> : null}</span><Check size={14} aria-hidden="true" />
        </button>)}
      </div> : null}
    </div>
  );
}
