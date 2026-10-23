"use client";

import { useEffect, useRef, useState } from "react";
import { Pencil, Play, Plus, Power, RefreshCw, Trash2, X } from "lucide-react";
import { connectionStatus, deleteMCPConnection, listMCPConnections, saveMCPConnection, testMCPConnection, type MCPConnection, type MCPConnectionInput } from "../../lib/mcp-connections";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";
import { MCPConnectionDialog } from "./MCPConnectionDialog";

export function MCPConnectionsPanel({ onCatalogChanged }: { onCatalogChanged: () => void }) {
  const readOnly = useWorkspaceReadOnly();
  const [connections, setConnections] = useState<MCPConnection[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [loading, setLoading] = useState(true);
  const [editing, setEditing] = useState<MCPConnection | null | undefined>();
  const [deleting, setDeleting] = useState<MCPConnection | null>(null);
  const [dialogError, setDialogError] = useState("");
  const inFlight = useRef(false);
  const alive = useRef(false);
  const reads = useRef<AbortController | null>(null);

  useEffect(() => {
    alive.current = true;
    const controller = new AbortController(); reads.current = controller;
    void listMCPConnections(controller.signal).then(items => { if (!controller.signal.aborted) setConnections(items); })
      .catch(err => { if (!controller.signal.aborted) setError(message(err)); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => { alive.current = false; reads.current?.abort(); };
  }, []);

  async function command(id: string, operation: () => Promise<MCPConnection | void>, modal = false) {
    if (inFlight.current || readOnly) return;
    inFlight.current = true; reads.current?.abort(); setLoading(false); setBusy(id); setError(""); setDialogError("");
    try {
      const saved = await operation();
      if (!alive.current) return;
      setConnections(current => saved ? [...current.filter(c => c.id !== saved.id), saved].sort((a, b) => a.created_at.localeCompare(b.created_at)) : current.filter(c => c.id !== id));
      if (modal) { setEditing(undefined); setDeleting(null); }
      onCatalogChanged();
    } catch (err) { if (alive.current) (modal ? setDialogError : setError)(message(err)); }
    finally { inFlight.current = false; if (alive.current) setBusy(""); }
  }

  async function refresh() {
    if (inFlight.current) return;
    const controller = new AbortController(); reads.current?.abort(); reads.current = controller;
    setLoading(true); setError("");
    try { const items = await listMCPConnections(controller.signal); if (!controller.signal.aborted) setConnections(items); }
    catch (err) { if (!controller.signal.aborted) setError(message(err)); }
    finally { if (!controller.signal.aborted) setLoading(false); }
  }

  function edit(connection: MCPConnection | null) { setDialogError(""); setEditing(connection); }
  function save(input: MCPConnectionInput) { void command(editing?.id ?? "new", () => saveMCPConnection(editing?.id ?? "", input), true); }

  return <section className="mcp-connections" aria-label="Workspace MCP connections">
    <header className="mcp-connections-heading"><div><h2>MCP connections</h2><p>Workspace-owned remote services · Streamable HTTP · read-only tools</p></div>
      <div className="mcp-actions"><button className="secondary-action" type="button" title="Refresh connections" aria-label="Refresh connections" disabled={!!busy || loading} onClick={() => void refresh()}><RefreshCw size={16} /></button>
        <button className="send" type="button" disabled={readOnly || !!busy} onClick={() => edit(null)}><Plus size={16} />Add connection</button></div>
    </header>
    {error ? <p className="error" role="alert">{error}</p> : null}
    {loading && !connections.length ? <p role="status">Loading connections...</p> : !connections.length && !error ? <p className="mcp-empty">No MCP connections in this Workspace.</p> : null}
    <div className="mcp-connection-list">{connections.map(connection => <article className="mcp-connection-row" key={connection.id} aria-label={connection.name}>
      <div className="mcp-connection-info"><h3>{connection.name}</h3><p>{connection.url}</p><small>{connection.auth_mode === "bearer" ? "Bearer token" : "No authentication"} · {connection.read_only_tools.length} selected / {connection.tools.length} discovered</small></div>
      <div className="mcp-connection-status"><span>{connectionStatus(connection)}</span>{connection.status === "discovered" ? <small>Tools discovered</small> : null}</div>
      <div className="mcp-actions">
        <button className="secondary-action" type="button" aria-label="Test connection" disabled={readOnly || !!busy} onClick={() => void command(connection.id, () => testMCPConnection(connection.id))}><Play size={15} />{busy === connection.id ? "Working..." : "Test"}</button>
        <button className="secondary-action" type="button" title="Edit connection" aria-label="Edit connection" disabled={readOnly || !!busy} onClick={() => edit(connection)}><Pencil size={15} /></button>
        <button className="secondary-action" type="button" title={connection.enabled ? "Disable connection" : "Enable connection"} aria-label={connection.enabled ? "Disable connection" : "Enable connection"}
          disabled={readOnly || !!busy || (!connection.enabled && (connection.status !== "discovered" || !connection.read_only_tools.length))}
          onClick={() => void command(connection.id, () => saveMCPConnection(connection.id, { name: connection.name, url: connection.url, auth_mode: connection.auth_mode, revision: connection.revision, read_only_tools: connection.read_only_tools, enabled: !connection.enabled }))}><Power size={15} /></button>
        <button className="secondary-action" type="button" title="Delete connection" aria-label="Delete connection" disabled={readOnly || !!busy} onClick={() => { setDialogError(""); setDeleting(connection); }}><Trash2 size={15} /></button>
      </div>
    </article>)}</div>
    {editing !== undefined ? <MCPConnectionDialog key={editing?.id ?? "new"} connection={editing} busy={!!busy} error={dialogError} onClose={() => { if (!inFlight.current) setEditing(undefined); }} onSave={save} /> : null}
    {deleting ? <DeleteConnectionDialog connection={deleting} busy={!!busy} error={dialogError} onClose={() => { if (!inFlight.current) setDeleting(null); }} onDelete={() => void command(deleting.id, () => deleteMCPConnection(deleting.id), true)} /> : null}
  </section>;
}

function DeleteConnectionDialog({ connection, busy, error, onClose, onDelete }: { connection: MCPConnection; busy: boolean; error: string; onClose: () => void; onDelete: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { const element = dialog.current!; element.showModal(); return () => element.close(); }, []);
  return <dialog ref={dialog} className="mcp-dialog mcp-delete-dialog" aria-label="Delete connection" onCancel={event => { event.preventDefault(); if (!busy) onClose(); }}>
    <h2>Delete {connection.name}?</h2><p>Disconnect this service and erase its saved token. Existing Run evidence is retained.</p>
    {error ? <p className="error" role="alert">{error}</p> : null}
    <footer className="mcp-dialog-actions"><button className="secondary-action" type="button" disabled={busy} onClick={onClose}><X size={15} />Cancel</button>
      <button className="danger-primary" type="button" disabled={busy} onClick={onDelete}><Trash2 size={15} />Delete connection</button></footer>
  </dialog>;
}

function message(error: unknown) { return error instanceof Error ? error.message : "MCP connection request failed"; }
