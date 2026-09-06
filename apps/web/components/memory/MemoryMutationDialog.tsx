"use client";

import { useEffect, useRef, useState } from "react";
import { RefreshCw, Save, Trash2, X } from "lucide-react";
import { APIError } from "../../lib/api-client";
import { getMemory, mutateMemory, type MemoryDetail, type MemoryInfo, type MemoryMutation } from "../../lib/memory-api";

export type MemoryDialogMode = "replace" | "delete" | "history";

export function MemoryMutationDialog({ memory, initialMode, onClose, onUpdated }: {
  memory: Pick<MemoryInfo, "id" | "content">;
  initialMode: MemoryDialogMode;
  onClose: () => void;
  onUpdated: (memory: MemoryInfo) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [detail, setDetail] = useState<MemoryDetail | null>(null);
  const [mode, setMode] = useState(initialMode);
  const [content, setContent] = useState(memory.content);
  const [actor, setActor] = useState("local-user");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [busy, setBusy] = useState(false);
  const inFlight = useRef(false);
  const previous = useRef<MemoryMutation | null>(null);

  useEffect(() => {
    const element = dialog.current!;
    element.showModal();
    let active = true;
    void getMemory(memory.id).then((value) => {
      if (!active) return;
      setDetail(value);
      setContent(value.memory.content);
    }).catch((err: unknown) => { if (active) setError(message(err)); });
    return () => { active = false; element.close(); };
  }, [memory.id]);

  async function refresh() {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    try {
      setDetail(await getMemory(memory.id));
      setConflict(false);
      setError("");
      previous.current = null;
    } catch (err) { setError(message(err)); }
    finally { inFlight.current = false; setBusy(false); }
  }

  async function submit() {
    if (!detail || mode === "history" || conflict || inFlight.current || detail.memory.deleted_at) return;
    const draft: MemoryMutation = {
      operation_id: previous.current?.operation_id ?? crypto.randomUUID(),
      expected_version: detail.memory.version, action: mode,
      ...(mode === "replace" ? { content: content.trim() } : {}),
      actor: actor.trim(), reason: reason.trim()
    };
    // Keep the command ID after a lost response; changed input is a new command.
    if (previous.current && JSON.stringify(previous.current) !== JSON.stringify(draft)) draft.operation_id = crypto.randomUUID();
    previous.current = draft;
    inFlight.current = true;
    setBusy(true);
    setError("");
    try {
      const result = await mutateMemory(memory.id, draft);
      setDetail({ memory: result.memory, changes: [result.change, ...detail.changes.filter((c) => c.operation_id !== result.change.operation_id)].sort((a, b) => b.version - a.version).slice(0, 100) });
      setMode("history");
      onUpdated(result.memory);
      try { setDetail(await getMemory(memory.id)); }
      catch { setError("Change saved. History could not be refreshed."); }
    } catch (err) {
      setConflict(err instanceof APIError && err.status === 409);
      setError(message(err));
    } finally { inFlight.current = false; setBusy(false); }
  }

  const current = detail?.memory;
  const title = mode === "history" ? "Memory history" : mode === "delete" ? "Delete memory" : "Correct memory";
  return <dialog ref={dialog} className="memory-dialog" aria-labelledby="memory-dialog-title" onCancel={(event) => { event.preventDefault(); if (!busy) onClose(); }}>
    <header className="memory-dialog-heading">
      <h2 id="memory-dialog-title">{title}</h2>
      <button className="secondary-action" type="button" aria-label="Close memory dialog" title="Close" disabled={busy} onClick={onClose}><X size={18} /></button>
    </header>
    <div className="memory-dialog-meta"><code>{memory.id}</code>{current ? <span>Version {current.version} · {current.deleted_at ? "Deleted" : "Active"}</span> : null}</div>
    {error ? <div className="error" role="alert">{error}</div> : null}
    {conflict ? <p role="status">This memory changed. Refresh its current version before applying your correction again. Your draft is preserved.</p> : null}
    {!detail || conflict || (error && mode === "history") ? <button className="secondary-action" type="button" disabled={busy} onClick={() => void refresh()}><RefreshCw size={15} /> Refresh current version</button> : null}
    {current ? <>
      {mode === "history" ? <>
        {current.deleted_at ? <p>Deleted from recall. Content and embedding have been removed.</p> : <p className="memory-current-content">{current.content}</p>}
        <h3>Recent changes</h3>
        {detail.changes.length === 0 ? <p>No corrections or deletions recorded.</p> : <ol className="memory-history">
          {detail.changes.map((change) => <li key={change.operation_id}>
            <strong>Version {change.previous_version} → {change.version} · {change.action === "replace" ? "Corrected" : "Deleted"}</strong>
            <span>{change.actor} · {new Date(change.created_at).toLocaleString()}</span>
            <p>{change.reason}</p>
            {change.source_message_id ? <code>Source message: {change.source_message_id}</code> : null}
          </li>)}
        </ol>}
      </> : current.deleted_at ? <p>This memory has already been deleted.</p> : <form onSubmit={(event) => { event.preventDefault(); void submit(); }}>
        <p className="memory-current-content">Current: {current.content}</p>
        {mode === "replace" ? <label className="memory-field memory-content-field"><span>Corrected content</span><textarea value={content} required maxLength={8000} disabled={busy} onChange={(e) => setContent(e.target.value)} /></label> : <p>Remove this memory from future recall? Its source conversation remains unchanged.</p>}
        <label className="memory-field"><span>Operator (self-reported)</span><input value={actor} required maxLength={128} disabled={busy} onChange={(e) => setActor(e.target.value)} /></label>
        <label className="memory-field"><span>Reason</span><textarea value={reason} required maxLength={512} disabled={busy} onChange={(e) => setReason(e.target.value)} /></label>
        <div className="memory-dialog-actions">
          <button className="secondary-action" type="button" disabled={busy} onClick={onClose}>Cancel</button>
          <button className={mode === "delete" ? "danger-primary" : "send memory-primary-action"} type="submit" disabled={busy || conflict || !actor.trim() || !reason.trim() || (mode === "replace" && !content.trim())}>
            {mode === "delete" ? <Trash2 size={15} /> : <Save size={15} />}{busy ? "Saving..." : mode === "delete" ? "Delete memory" : "Save correction"}
          </button>
        </div>
      </form>}
    </> : <p role="status">{error ? "Memory could not be loaded." : "Loading memory..."}</p>}
  </dialog>;
}

function message(error: unknown) { return error instanceof Error ? error.message : "Memory request failed"; }
