"use client";

import { useEffect, useRef, useState } from "react";
import { Archive, Pencil, Plus, RotateCcw, Save, Star, Trash2, X } from "lucide-react";
import { createWorkspace, deleteWorkspace, listWorkspaces, updateWorkspace, type Workspace } from "../../lib/workspaces";

type Editor = { kind: "create" } | { kind: "edit" | "archive" | "delete"; workspace: Workspace };

export function WorkspaceSettings({ workspaces, onChanged, onClose }: {
  workspaces: Workspace[];
  onChanged: (workspaces: Workspace[]) => void;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const locked = useRef(false);
  const mounted = useRef(true);
  const [editor, setEditor] = useState<Editor | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [replacement, setReplacement] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    const element = dialog.current!;
    mounted.current = true;
    element.showModal();
    return () => { mounted.current = false; element.close(); };
  }, []);

  function open(next: Editor) {
    setEditor(next);
    setName(next.kind === "create" ? "" : next.workspace.name);
    setDescription(next.kind === "create" ? "" : next.workspace.description);
    setReplacement(""); setError("");
  }

  async function mutate(action: () => Promise<unknown>) {
    if (locked.current) return;
    locked.current = true; setBusy(true); setError("");
    let saved = false;
    try {
      await action();
      saved = true;
      if (mounted.current) setEditor(null);
      // Notify from a fresh authoritative list, never optimistic defaults/status.
      const items = await listWorkspaces();
      if (mounted.current) { onChanged(items); setEditor(null); }
    } catch (err) { if (mounted.current) setError(`${saved ? "Change saved, but the Workspace list could not be refreshed. " : ""}${err instanceof Error ? err.message : "Workspace change failed"}`); }
    finally { locked.current = false; if (mounted.current) setBusy(false); }
  }

  function submit() {
    if (!editor) return;
    if (editor.kind === "create") void mutate(() => createWorkspace({ name: name.trim(), description: description.trim() }));
    else if (editor.kind === "edit") void mutate(() => updateWorkspace(editor.workspace.id, { name: name.trim(), description: description.trim() }));
    else if (editor.kind === "archive") void mutate(() => updateWorkspace(editor.workspace.id, { status: "archived", replacement_workspace_id: replacement }));
    else void mutate(() => deleteWorkspace(editor.workspace.id, replacement));
  }

  const closing = editor?.kind === "delete" || editor?.kind === "archive";
  const needsReplacement = closing && editor.workspace.is_default;
  const alternatives = closing ? workspaces.filter(item => item.id !== editor.workspace.id && item.status === "active") : [];
  const requiresConfirmation = closing && (editor.kind === "delete" || needsReplacement);
  return <dialog ref={dialog} className="workspace-settings" aria-labelledby="workspace-settings-title" onCancel={event => { event.preventDefault(); if (!locked.current) onClose(); }}>
    <header className="workspace-settings-heading"><h2 id="workspace-settings-title">Workspaces</h2><button className="secondary-action" type="button" title="Close" aria-label="Close workspace settings" disabled={busy} onClick={onClose}><X size={18} /></button></header>
    {error ? <><p className="error" role="alert">{error}</p><button className="secondary-action" type="button" disabled={busy} onClick={() => void mutate(async () => {})}><RotateCcw size={15} /> Refresh workspaces</button></> : null}
    {!editor ? <>
      <div className="workspace-settings-toolbar"><span>{workspaces.length} Workspaces</span><button className="secondary-action" type="button" disabled={busy} onClick={() => open({ kind: "create" })}><Plus size={15} /> New workspace</button></div>
      <table className="workspace-settings-table"><thead><tr><th>Name</th><th>Status</th><th>Actions</th></tr></thead><tbody>
        {workspaces.map(item => <tr key={item.id}><td><strong>{item.name}</strong>{item.is_default ? <span className="workspace-default">Default</span> : null}<p>{item.description}</p></td><td>{item.status === "active" ? "Active" : "Archived"}</td><td><div className="workspace-row-actions">
          <button type="button" disabled={busy} aria-label="Edit workspace" title="Edit workspace" onClick={() => open({ kind: "edit", workspace: item })}><Pencil size={16} /></button>
          <button type="button" disabled={busy || item.is_default || item.status !== "active"} aria-label="Make default workspace" title="Make default workspace" onClick={() => void mutate(() => updateWorkspace(item.id, { make_default: true }))}><Star size={16} /></button>
          {item.status === "active" ? <button type="button" disabled={busy} aria-label="Archive workspace" title="Archive workspace" onClick={() => open({ kind: "archive", workspace: item })}><Archive size={16} /></button> : <button type="button" disabled={busy} aria-label="Restore workspace" title="Restore workspace" onClick={() => void mutate(() => updateWorkspace(item.id, { status: "active" }))}><RotateCcw size={16} /></button>}
          <button type="button" disabled={busy} aria-label="Delete workspace" title="Delete workspace" onClick={() => open({ kind: "delete", workspace: item })}><Trash2 size={16} /></button>
        </div></td></tr>)}
      </tbody></table>
    </> : <form onSubmit={event => { event.preventDefault(); submit(); }}>
      <h3>{editor.kind === "create" ? "New workspace" : editor.kind === "edit" ? "Edit workspace" : `${editor.kind === "delete" ? "Delete" : "Archive"} ${editor.workspace.name}`}</h3>
      {!closing ? <>
        <label className="workspace-settings-field"><span>Name</span><input autoFocus required maxLength={80} disabled={busy} value={name} onChange={event => setName(event.target.value)} /></label>
        <label className="workspace-settings-field"><span>Description</span><textarea maxLength={2000} rows={3} disabled={busy} value={description} onChange={event => setDescription(event.target.value)} /></label>
      </> : <>
        <p>{editor.kind === "delete" ? "This Workspace will no longer be accessible. Its stored data is retained; deletion cannot be undone here." : "This Workspace becomes read-only until you restore it."}</p>
        {needsReplacement ? <label className="workspace-settings-field"><span>Replacement default workspace</span><select required disabled={busy} value={replacement} onChange={event => setReplacement(event.target.value)}><option value="">Select an active Workspace</option>{alternatives.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label> : null}
        {alternatives.length === 0 ? <p role="status">Create another active Workspace before closing this one.</p> : null}
      </>}
      <footer className="workspace-settings-actions"><button className="secondary-action" type="button" disabled={busy} onClick={() => { setEditor(null); setError(""); }}>Cancel</button><button className={editor.kind === "delete" ? "danger-primary" : "send"} type="submit" disabled={busy || (!closing && !name.trim()) || (closing && (!alternatives.length || (needsReplacement && !replacement)))}>{editor.kind === "delete" ? <Trash2 size={15} /> : <Save size={15} />}{busy ? "Saving..." : requiresConfirmation ? editor.kind === "delete" ? "Confirm deletion" : "Confirm archive" : editor.kind === "create" ? "Create workspace" : editor.kind === "edit" ? "Save changes" : "Archive workspace"}</button></footer>
    </form>}
  </dialog>;
}
