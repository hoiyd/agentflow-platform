"use client";

import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { FileText, RotateCcw, X } from "lucide-react";
import type { components } from "../../lib/api-contract.gen";
import type { ToolEffect } from "../../lib/api";
import { getToolCompensationPreview, listToolEffects, reconcileToolEffect } from "../../lib/api";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";

type Preview = components["schemas"]["CompensationPreview"];

export function FileUndoPanel({ runId, onChanged }: { runId: string; onChanged: () => Promise<void> }) {
  const [effects, setEffects] = useState<ToolEffect[]>([]);
  const [selected, setSelected] = useState<ToolEffect>();
  const [error, setError] = useState("");
  const [revision, refresh] = useState(0);
  const readOnly = useWorkspaceReadOnly();
  async function refreshEvidence() {
    refresh(value => value + 1);
    try { await onChanged(); }
    catch (err) { setError(`Replay refresh unavailable: ${err instanceof Error ? err.message : "reload the run"}`); }
  }
  useEffect(() => {
    let current = true;
    void listToolEffects(runId).then(items => {
      if (current) {
        setEffects(items.filter(item => ["file_write", "file_patch"].includes(item.tool_name) &&
          (item.status === "compensated" || (item.status === "committed" && item.available_actions?.includes("compensate")))));
        setError("");
      }
    }).catch(err => { if (current) setError(err instanceof Error ? err.message : "File changes unavailable"); });
    return () => { current = false; };
  }, [runId, revision]);
  if (!effects.length && !error) return null;
  return <section className="file-undo-panel" aria-label="File changes">
    <header><h2><FileText size={17} aria-hidden="true" />File changes</h2>{error ? <p role="alert">{error}</p> : null}</header>
    {effects.map(effect => <div className="file-undo-row" key={effect.idempotency_key}>
      <div><strong>{effect.tool_name}</strong><code>{effect.tool_call_id}</code></div>
      {effect.status === "compensated" ? <span>Undone</span> : <button type="button" className="secondary-action" disabled={readOnly}
        onClick={() => setSelected(effect)}><RotateCcw size={14} aria-hidden="true" />Review undo</button>}
    </div>)}
    {selected ? <FileUndoDialog key={selected.idempotency_key} runId={runId} effect={selected}
      onClose={() => { setSelected(undefined); void refreshEvidence(); }}
      onApplied={async () => { setEffects(items => items.map(item => item.idempotency_key === selected.idempotency_key ? { ...item, status: "compensated" } : item)); setSelected(undefined); await refreshEvidence(); }} /> : null}
  </section>;
}

function FileUndoDialog({ runId, effect, onClose, onApplied }: {
  runId: string; effect: ToolEffect; onClose: () => void; onApplied: () => Promise<void>;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const admitted = useRef(false);
  const title = useId();
  const [preview, setPreview] = useState<Preview>();
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const readOnly = useWorkspaceReadOnly();
  useLayoutEffect(() => {
    const element = dialog.current!;
    const opener = document.activeElement as HTMLElement | null;
    element.showModal();
    return () => { element.close(); opener?.focus(); };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void getToolCompensationPreview(runId, effect.idempotency_key, controller.signal).then(value => {
      if (!controller.signal.aborted) setPreview(value);
    }).catch(err => { if (!controller.signal.aborted) setError(err instanceof Error ? err.message : "Undo preview unavailable"); });
    return () => controller.abort();
  }, [runId, effect.idempotency_key]);
  async function apply() {
    if (admitted.current || readOnly || !preview?.available || !reason.trim()) return;
    admitted.current = true;
    setBusy(true);
    setError("");
    try {
      const result = await reconcileToolEffect(runId, effect.idempotency_key, {
        command_id: crypto.randomUUID(), action: "compensate", expected_version: effect.version,
        actor: "current-owner", reason: reason.trim()
      });
      if (result.outcome !== "completed" || result.effect.status !== "compensated") {
        setPreview(current => current ? { ...current, available: false } : current);
        setError(result.effect.error || "Undo needs verification. Review Tool effect reconciliation before trying again.");
        return;
      }
      await onApplied();
    } catch (err) {
      setPreview(current => current ? { ...current, available: false } : current);
      setError(err instanceof Error ? err.message : "Undo failed; review the current file and effect before retrying");
    } finally { admitted.current = false; setBusy(false); }
  }
  return <dialog ref={dialog} className="file-undo-dialog" aria-labelledby={title}
    onCancel={event => { event.preventDefault(); if (!admitted.current) onClose(); }}>
    <header><h2 id={title}>Undo file change</h2><button type="button" className="file-undo-close" disabled={busy} aria-label="Close undo preview" onClick={onClose}><X size={18} /></button></header>
    {!preview && !error ? <p role="status">Checking retained version and current file...</p> : null}
    {error ? <p className="error" role="alert">{error}</p> : null}
    {preview ? <>
      {preview.resource ? <strong className="file-undo-resource">{preview.resource}</strong> : null}
      {!preview.available ? <p role="status">{preview.unavailable_reason || "Undo unavailable. Refresh the run to inspect its current state."}</p> : null}
      {preview.before_hash ? <dl className="file-undo-versions">
        <div><dt>Written version</dt><dd>{preview.after_bytes.toLocaleString()} bytes<code>{preview.after_hash}</code></dd></div>
        <div><dt>Restore version</dt><dd>{preview.before_bytes.toLocaleString()} bytes<code>{preview.before_hash}</code></dd></div>
      </dl> : null}
      {preview.before_hash ? <p className="file-undo-expiry">Retained until {new Date(preview.expires_at).toLocaleString()}</p> : null}
      {preview.available ? <label className="file-undo-reason">Reason<input maxLength={512} value={reason} onChange={event => setReason(event.target.value)} /></label> : null}
    </> : null}
    <footer><button type="button" className="secondary-action" disabled={busy} onClick={onClose}>Cancel</button>
      <button type="button" className="send" disabled={readOnly || busy || !preview?.available || !reason.trim()} onClick={() => void apply()}><RotateCcw size={15} />{busy ? "Undoing..." : "Undo change"}</button></footer>
  </dialog>;
}
