"use client";

import { useEffect, useRef, useState } from "react";
import { Save, X } from "lucide-react";
import { SelectionMenu } from "../ui/SelectionMenu";
import type { MCPConnection, MCPConnectionInput } from "../../lib/mcp-connections";

export function MCPConnectionDialog({ connection, busy, error, onClose, onSave }: {
  connection: MCPConnection | null; busy: boolean; error: string;
  onClose: () => void; onSave: (input: MCPConnectionInput) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [name, setName] = useState(connection?.name ?? "");
  const [url, setURL] = useState(connection?.url ?? "");
  const [auth, setAuth] = useState<MCPConnectionInput["auth_mode"]>(connection?.auth_mode ?? "none");
  const [token, setToken] = useState("");
  const [selected, setSelected] = useState(connection?.read_only_tools ?? []);
  const [enabled, setEnabled] = useState(connection?.enabled ?? false);
  const identityChanged = url !== connection?.url || auth !== connection?.auth_mode || token !== "";
  const tools = identityChanged ? [] : connection?.tools ?? [];
  const ready = connection?.status === "discovered" && !identityChanged;

  useEffect(() => {
    const element = dialog.current!;
    element.showModal();
    return () => element.close();
  }, []);

  function submit(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    onSave({ name: name.trim(), url: url.trim(), auth_mode: auth,
      enabled: ready && enabled, read_only_tools: ready ? selected : [],
      ...(connection ? { revision: connection.revision } : {}),
      ...(auth === "bearer" && token ? { bearer_token: token } : {}) });
  }

  return <dialog ref={dialog} className="mcp-dialog" aria-labelledby="mcp-connection-title"
    onCancel={event => { event.preventDefault(); if (!busy) onClose(); }}>
    <form onSubmit={submit}>
      <header className="mcp-dialog-heading"><h2 id="mcp-connection-title">{connection ? "Edit connection" : "Add MCP connection"}</h2>
        <button type="button" className="secondary-action" aria-label="Close connection dialog" title="Close" onClick={onClose} disabled={busy}><X size={18} /></button>
      </header>
      {error ? <p className="error" role="alert">{error}</p> : null}
      <fieldset disabled={busy} className="mcp-fields">
        <label>Connection name<input value={name} required maxLength={80} autoFocus onChange={e => setName(e.target.value)} /></label>
        <label>Endpoint URL<input value={url} required type="url" maxLength={2048} placeholder="https://service.example/mcp" onChange={e => setURL(e.target.value)} /></label>
        <SelectionMenu label="Authentication" value={auth} searchable={false} disabled={busy}
          options={[{ value: "none", label: "None" }, { value: "bearer", label: "Bearer token" }]}
          onChange={value => { setAuth(value as MCPConnectionInput["auth_mode"]); setToken(""); }} />
        {auth === "bearer" ? <label>Bearer token<input type="password" autoComplete="new-password" value={token} maxLength={8192}
          required={!connection?.credential_configured || url !== connection.url}
          placeholder={connection?.credential_configured ? "Leave blank to keep saved token" : "Token"} onChange={e => setToken(e.target.value)} /></label> : null}
        {tools.length ? <fieldset className="mcp-tool-selection"><legend>Reviewed read-only tools</legend>
          {tools.map(tool => <label key={tool.name} className="mcp-tool-option">
            <input type="checkbox" aria-label={`Allow ${tool.name}`} disabled={!tool.supported} checked={selected.includes(tool.name)}
              onChange={e => setSelected(current => e.target.checked ? [...current, tool.name] : current.filter(name => name !== tool.name))} />
            <span><strong>{tool.name}</strong><small>{tool.supported ? tool.description : tool.reason?.replaceAll("_", " ")}</small></span>
          </label>)}
        </fieldset> : null}
        <label className="mcp-enabled"><input type="checkbox" checked={ready && enabled} disabled={!ready || selected.length === 0}
          onChange={e => setEnabled(e.target.checked)} /> Enable connection</label>
      </fieldset>
      <footer className="mcp-dialog-actions">
        <button type="button" className="secondary-action" disabled={busy} onClick={onClose}><X size={15} />Cancel</button>
        <button type="submit" className="send" disabled={busy || !name.trim() || !url.trim()}><Save size={15} />{busy ? "Saving..." : "Save connection"}</button>
      </footer>
    </form>
  </dialog>;
}
