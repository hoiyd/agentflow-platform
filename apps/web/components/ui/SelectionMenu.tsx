"use client";

import { useEffect, useId, useRef, useState, type KeyboardEvent } from "react";
import { Check, ChevronDown, Search } from "lucide-react";
import { DisabledControlNote } from "./DisabledControlNote";

type SelectionOption = {
  value: string;
  label: string;
  description?: string;
  annotation?: string;
};

type SelectionMenuProps = {
  label: string;
  value: string;
  options: SelectionOption[];
  onChange: (value: string) => void;
  disabled?: boolean;
  placement?: "above" | "below";
  className?: string;
  optionsLabel?: string;
  searchable?: boolean;
  disabledNote?: string;
};

export function SelectionMenu({ label, value, options, onChange, disabled = false, placement = "below", className = "", optionsLabel = `${label.toLowerCase()}s`, searchable = true, disabledNote }: SelectionMenuProps) {
  const [open, setOpen] = useState(false);
  const unavailable = disabled || options.length === 0;
  const [previous, setPrevious] = useState({ value, unavailable });
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const listID = useId();
  const selected = options.find(item => item.value === value);

  // Discard a popup during a selection/scope change or when a Run locks it.
  if (previous.value !== value || previous.unavailable !== unavailable) {
    setPrevious({ value, unavailable });
    setOpen(false);
  }

  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => {
      if (!root.current?.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);

  function close(restoreFocus = false) {
    setOpen(false);
    if (restoreFocus) trigger.current?.focus();
  }

  const triggerLabel = `${label}: ${selected?.label ?? `Select ${label.toLowerCase()}`}`;
  const triggerButton = (
    <button className="selection-trigger" ref={trigger} type="button" disabled={unavailable}
      aria-label={triggerLabel} title={unavailable && disabledNote ? undefined : selected?.label}
      aria-haspopup="listbox" aria-expanded={open && !unavailable}
      aria-controls={open && !unavailable ? listID : undefined}
      onClick={() => setOpen(current => !current)}
      onKeyDown={event => {
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          setOpen(true);
        }
      }}>
      <span>{selected?.label ?? `Select ${label.toLowerCase()}`}</span>
      <ChevronDown size={14} aria-hidden="true" />
    </button>
  );

  return (
    <div className={`selection-menu ${className}`} ref={root}
      onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget)) close(); }}
      onKeyDown={event => {
        if (event.key === "Escape" && open && !event.nativeEvent.isComposing && event.nativeEvent.keyCode !== 229) {
          event.preventDefault();
          event.stopPropagation();
          close(true);
        }
      }}>
      <span className="selection-label">{label}</span>
      {unavailable && disabledNote ? (
        <DisabledControlNote key={disabledNote} label={triggerLabel} note={disabledNote}>{triggerButton}</DisabledControlNote>
      ) : triggerButton}
      {open && !unavailable ? <SelectionPopup label={label} optionsLabel={optionsLabel} searchable={searchable} value={value} options={options} listID={listID} placement={placement}
        onSelect={next => {
          close(true);
          if (next !== value) onChange(next);
        }} /> : null}
    </div>
  );
}

function SelectionPopup({label, optionsLabel, searchable, value, options, listID, placement, onSelect}: {
  label: string; optionsLabel: string; searchable: boolean; value: string; options: SelectionOption[]; listID: string;
  placement: "above" | "below"; onSelect: (value: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [activeValue, setActiveValue] = useState(value);
  const search = useRef<HTMLInputElement>(null);
  const listbox = useRef<HTMLDivElement>(null);
  const normalized = query.trim().toLocaleLowerCase();
  const visible = options.filter(item => `${item.label} ${item.description ?? ""} ${item.annotation ?? ""}`.toLocaleLowerCase().includes(normalized));
  const activeIndex = Math.max(0, visible.findIndex(item => item.value === activeValue));
  const activeID = visible.length ? `${listID}-${activeIndex}` : undefined;

  useEffect(() => { (searchable ? search.current : listbox.current)?.focus(); }, [searchable]);
  useEffect(() => { if (activeID) document.getElementById(activeID)?.scrollIntoView?.({block: "nearest"}); }, [activeID]);

  function navigate(event: KeyboardEvent) {
    if (event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      if (visible.length) setActiveValue(visible[(activeIndex + (event.key === "ArrowDown" ? 1 : visible.length - 1)) % visible.length].value);
    } else if (event.key === "Enter" || (!searchable && event.key === " ")) {
      event.preventDefault();
      if (visible[activeIndex]) onSelect(visible[activeIndex].value);
    }
  }

  return (
    <div className="selection-popup" data-placement={placement} data-searchable={searchable} onKeyDown={navigate}>
      {searchable ? <div className="selection-search">
        <Search size={15} aria-hidden="true" />
        <input ref={search} role="combobox" aria-label={`Search ${optionsLabel}`}
          aria-controls={listID} aria-expanded="true" aria-autocomplete="list" aria-activedescendant={activeID}
          placeholder={`Search ${optionsLabel}...`} autoComplete="off" spellCheck={false} value={query}
          onChange={event => { setQuery(event.target.value); setActiveValue(""); }} />
      </div> : null}
      <div className="selection-options" id={listID} ref={listbox} role="listbox" aria-label={label}
        tabIndex={searchable ? undefined : 0} aria-activedescendant={searchable ? undefined : activeID}>
        {visible.map((item, index) => <button className="selection-option" id={`${listID}-${index}`} key={item.value}
          type="button" role="option" aria-selected={item.value === value} tabIndex={-1}
          data-active={index === activeIndex} onPointerMove={() => setActiveValue(item.value)} onClick={() => onSelect(item.value)}>
          <span className="selection-option-copy">
            <span className="selection-option-name">{item.label}</span>
            {item.annotation ? <small>{item.annotation}</small> : null}
            {item.description ? <span className="selection-option-description">{item.description}</span> : null}
          </span>
          <Check size={15} aria-hidden="true" />
        </button>)}
        {!visible.length ? <p className="selection-empty" role="status">No {optionsLabel} found</p> : null}
      </div>
    </div>
  );
}
