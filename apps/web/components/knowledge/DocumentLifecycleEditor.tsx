"use client";

import { useEffect, useRef, useState } from "react";
import { RotateCcw, Save } from "lucide-react";
import type { components } from "../../lib/api-contract.gen";
import { updateDocumentMetadata, type DocumentInfo } from "../../lib/knowledge-api";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";
import { SelectionMenu } from "../ui/SelectionMenu";

type Lifecycle = components["schemas"]["DocumentLifecycle"];
const statuses = [
  { value: "active", label: "Active" },
  { value: "draft", label: "Draft" },
  { value: "withdrawn", label: "Withdrawn" }
];
const dates = [
  ["valid_from", "Valid from (UTC)"],
  ["valid_until", "Valid until (UTC)"],
  ["published_at", "Published at (UTC)"]
] as const;

export function DocumentLifecycleEditor({ document, onSaved, onReload }: {
  document: DocumentInfo;
  onSaved: (document: DocumentInfo, expectedUpdatedAt: string) => void;
  onReload: () => void;
}) {
  const readOnly = useWorkspaceReadOnly();
  const original: Lifecycle = { status: "active", ...document.lifecycle };
  const [draft, setDraft] = useState(original);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const pending = useRef<AbortController | null>(null);
  useEffect(() => () => pending.current?.abort(), []);
  const dirty = JSON.stringify(draft) !== JSON.stringify(original);
  const availability = currentAvailability(original);

  async function save() {
    if (readOnly || pending.current || !dirty) return;
    const controller = new AbortController();
    pending.current = controller;
    setSaving(true);
    setError("");
    try {
      const updated = await updateDocumentMetadata(document.id, {
        expected_updated_at: document.updated_at, metadata: document.metadata, lifecycle: draft
      }, controller.signal);
      if (!controller.signal.aborted) onSaved(updated, document.updated_at);
    } catch (failure) {
      if (!controller.signal.aborted) setError(failure instanceof Error ? failure.message : "Failed to save lifecycle");
    } finally {
      if (!controller.signal.aborted) { pending.current = null; setSaving(false); }
    }
  }

  return (
    <section className="document-lifecycle" aria-label="Document lifecycle">
      <div className="document-lifecycle-heading"><h3>Lifecycle</h3><span>{availability}</span></div>
      <form onSubmit={event => { event.preventDefault(); void save(); }}>
        <div className="document-lifecycle-fields">
          <SelectionMenu label="Status" value={draft.status ?? "active"} options={statuses} searchable={false}
            disabled={readOnly || saving} onChange={value => setDraft(current => ({ ...current, status: value as Lifecycle["status"] }))} />
          {dates.map(([key, label]) => (
            <label key={key}>{label}<input type="datetime-local" step="0.000001" min="0001-01-01T00:00" max="9999-12-31T23:59:59.999999"
              disabled={readOnly || saving} value={draft[key]?.replace(/Z$/, "") ?? ""}
              onChange={event => { const value = event.target.value; setDraft(current => {
                const next = { ...current };
                if (value) next[key] = `${value.length === 16 ? `${value}:00` : value}Z`; else delete next[key];
                return next;
              }); }} /></label>
          ))}
        </div>
        {error ? <p className="error" role="alert">{error}</p> : null}
        <div className="document-lifecycle-actions">
          {error ? <button className="secondary-action" type="button" onClick={onReload}><RotateCcw size={14} aria-hidden="true" />Reload document</button> : null}
          <button className="secondary-action" type="button" disabled={!dirty || saving} onClick={() => { setDraft(original); setError(""); }}><RotateCcw size={14} aria-hidden="true" />Reset</button>
          <button className="secondary-action" type="submit" disabled={readOnly || saving || !dirty}><Save size={14} aria-hidden="true" />{saving ? "Saving..." : "Save lifecycle"}</button>
        </div>
      </form>
    </section>
  );
}

function currentAvailability(value: Lifecycle) {
  if (value.status === "withdrawn") return "Withdrawn";
  if (value.status === "draft") return "Draft";
  const now = Date.now();
  if (value.valid_from && Date.parse(value.valid_from) > now) return "Scheduled";
  if (value.valid_until && Date.parse(value.valid_until) <= now) return "Expired";
  return "Available";
}
