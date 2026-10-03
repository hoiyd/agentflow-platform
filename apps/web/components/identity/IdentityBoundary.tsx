"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";
import { LogOut, Settings } from "lucide-react";
import { apiURL, setWorkspaceID } from "../../lib/api-client";
import { getIdentitySession, signOut, type IdentitySession } from "../../lib/identity";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";
import { WorkspaceSettings } from "./WorkspaceSettings";
import { WorkspaceReadOnly, ServiceConfigurationReadOnly, TrustedHostCommands } from "./WorkspaceContext";
import { listWorkspaces, type Workspace } from "../../lib/workspaces";

const workspaceKey = "agentflow-workspace";

export function IdentityBoundary({ children }: { children: ReactNode }) {
  const isPublic = usePathname() === "/";
  const [session, setSession] = useState<IdentitySession | null>(null);
  const [workspace, setWorkspace] = useState("");
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const loginRedirectStarted = useRef(false);

  useEffect(() => {
    if (isPublic) return;
    const controller = new AbortController();
    void getIdentitySession(controller.signal).then(async info => {
      if (controller.signal.aborted) return;
      if (info.mode !== "local" && info.mode !== "oidc" || !Array.isArray(info.workspaces)) {
        throw new Error("Invalid identity session response");
      }
      const items = info.mode === "local" || info.authenticated ? await listWorkspaces(controller.signal) : [];
      if (controller.signal.aborted) return;
      const remembered = sessionStorage.getItem(workspaceKey);
      const selected = remembered && items.some(item => item.id === remembered) ? remembered : items.find(item => item.is_default)?.id ?? items.find(item => item.status === "active")?.id ?? "";
      setWorkspaceID(selected);
      setWorkspace(selected);
      setWorkspaces(items);
      setSession(info);
      setError("");
    }).catch(err => {
      if (!controller.signal.aborted) { setSession(null); setError(err instanceof Error ? err.message : "Failed to check session"); }
    });
    const expire = () => { controller.abort(); setWorkspaceID(""); setWorkspaces([]); setSettingsOpen(false); setSession({ mode: "oidc", authenticated: false, user: null, workspaces: [] }); setError(""); };
    window.addEventListener("agentflow-auth-required", expire);
    return () => { controller.abort(); window.removeEventListener("agentflow-auth-required", expire); };
  }, [isPublic, retry]);

  useEffect(() => {
    if (!isPublic && !error && session?.mode === "oidc" && !session.authenticated && !loginRedirectStarted.current) {
      loginRedirectStarted.current = true;
      globalThis.location.replace(apiURL("/api/auth/login"));
    }
  }, [isPublic, session, error]);

  async function logout() {
    setBusy(true);
    try {
      await signOut();
      sessionStorage.removeItem(workspaceKey);
      window.location.reload();
    } catch (err) { setError(err instanceof Error ? err.message : "Failed to sign out"); }
    finally { setBusy(false); }
  }

  function updated(items: Workspace[]) {
    setWorkspaces(items);
    if (!items.some(item => item.id === workspace)) {
      const id = items.find(item => item.is_default)?.id ?? items.find(item => item.status === "active")?.id;
      if (id) { sessionStorage.setItem(workspaceKey, id); window.location.assign("/workspace"); }
    }
  }
  const settings = settingsOpen ? <WorkspaceSettings workspaces={workspaces} onChanged={updated} onClose={() => setSettingsOpen(false)} /> : null;
  if (isPublic) return children;
  const allowed = (session?.authenticated || session?.mode === "local") && workspaces.length > 0;
  const unauthenticated = session?.mode === "oidc" && !session.authenticated;

  // No intermediate sign-in screen. Keep business consumers unmounted during
  // the session probe/redirect; only errors and authenticated nonmembers stay here.
  if (!error && (!session || unauthenticated)) return null;
  if (!allowed) {
    return (
      <main className="identity-gate">
        <section>
          <Link className="identity-brand" href="/">AgentFlow</Link>
          <h1>{error ? "Connection unavailable" : "Workspace access required"}</h1>
          {error ? <p role="alert">{error}</p> : session?.authenticated ? <p>No Workspace membership is assigned to this account.</p> : null}
          {session?.authenticated ? <><code>{session.user?.subject}</code><button onClick={() => setSettingsOpen(true)} type="button">Workspace settings</button><button disabled={busy} onClick={() => void logout()} type="button"><LogOut size={16} /> Sign out</button></> : <button onClick={() => setRetry(value => value + 1)} type="button">Retry</button>}
          {settings}
        </section>
      </main>
    );
  }
  return (
    <div className="identity-workspace">
      <header className="identity-toolbar">
        <span>{session?.mode === "local" ? "Super (local)" : session?.user?.name || session?.user?.id}</span>
        <WorkspaceSwitcher workspaces={workspaces} selected={workspace}
          onChange={id => { sessionStorage.setItem(workspaceKey, id); window.location.assign("/workspace"); }} />
        <button type="button" title="Workspace settings" aria-label="Workspace settings" onClick={() => setSettingsOpen(true)}><Settings size={15} /></button>
        {session?.mode === "oidc" ? <button disabled={busy} onClick={() => void logout()} type="button"><LogOut size={14} /> Sign out</button> : null}
      </header>
      {error ? <p className="identity-error" role="alert">{error}</p> : null}
      {workspaces.find(item => item.id === workspace)?.status === "archived" ? <div className="workspace-readonly" role="status">Archived Workspace · Read-only<button type="button" onClick={() => setSettingsOpen(true)}>Workspace settings</button></div> : null}
      <TrustedHostCommands.Provider value={session?.mode === "local"}><ServiceConfigurationReadOnly.Provider value={session?.mode === "oidc"}><WorkspaceReadOnly.Provider value={workspaces.find(item => item.id === workspace)?.status === "archived"}><div className="identity-content" key={workspace}>{children}</div></WorkspaceReadOnly.Provider></ServiceConfigurationReadOnly.Provider></TrustedHostCommands.Provider>
      {settings}
    </div>
  );
}
