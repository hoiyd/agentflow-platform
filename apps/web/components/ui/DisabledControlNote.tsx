"use client";

import { useId, useState, type ReactNode } from "react";

export function DisabledControlNote({ label, note, children }: { label: string; note: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const id = useId();

  // The wrapper receives hover and focus because native disabled controls cannot.
  return (
    <span className="disabled-control-note" role="group" aria-label={label} aria-disabled="true"
      tabIndex={0} aria-describedby={open ? id : undefined} data-open={open}
      onPointerEnter={() => setOpen(true)}
      onPointerLeave={event => { if (!event.currentTarget.contains(document.activeElement)) setOpen(false); }}
      onFocus={() => setOpen(true)}
      onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false); }}
      onKeyDown={event => {
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
        }
      }}>
      {children}
      {open ? <span className="disabled-control-note-box" role="tooltip" id={id}>{note}</span> : null}
    </span>
  );
}
