"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import Link from "next/link";
import { LogOut } from "lucide-react";
import { apiURL, setWorkspaceID } from "../../lib/api-client";
import { getIdentitySession, signOut, type IdentitySession } from "../../lib/identity";
import { WorkspaceSwitcher } from "./WorkspaceSwitcher";

const workspaceKey = "agentflow-workspace";

export function IdentityBoundary({ children }: { children: ReactNode }) {
  const isPublic = usePathname() === "/";
  const [session, setSession] = useState<IdentitySession | null>(null);
  const [workspace, setWorkspace] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [retry, setRetry] = useState(0);
  const loginRedirectStarted = useRef(false);

  useEffect(() => {
    if (isPublic) return;
    const controller = new AbortController();
    void getIdentitySession(controller.signal).then(info => {
      if (controller.signal.aborted) return;
      if (info.mode !== "local" && info.mode !== "oidc" || !Array.isArray(info.workspaces)) {
        throw new Error("Invalid identity session response");
      }
      const remembered = sessionStorage.getItem(workspaceKey);
      const selected = remembered && info.workspaces.includes(remembered) ? remembered : info.workspaces[0] ?? "";
      if (info.mode === "oidc" && selected) setWorkspaceID(selected);
      setWorkspace(selected);
      setSession(info);
      setError("");
    }).catch(err => {
      if (!controller.signal.aborted) { setSession(null); setError(err instanceof Error ? err.message : "Failed to check session"); }
    });
    const expire = () => { controller.abort(); setSession(previous => ({ mode: "oidc", authenticated: false, user: null, workspaces: [], registration_enabled: previous?.registration_enabled })); setError(""); };
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

  if (isPublic || session?.mode === "local") return children;
  const allowed = session?.authenticated && session.workspaces.length > 0;
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
          {session?.authenticated ? <><code>{session.user?.subject}</code><button disabled={busy} onClick={() => void logout()} type="button"><LogOut size={16} /> Sign out</button></> : <button onClick={() => setRetry(value => value + 1)} type="button">Retry</button>}
        </section>
      </main>
    );
  }
  return (
    <div className="identity-workspace">
      <header className="identity-toolbar">
        <span>{session.user?.name || session.user?.id}</span>
        <WorkspaceSwitcher workspaces={session.workspaces} selected={workspace} personalWorkspace={session.personal_workspace}
          onChange={id => { sessionStorage.setItem(workspaceKey, id); window.location.assign("/workspace"); }} />
        <button disabled={busy} onClick={() => void logout()} type="button"><LogOut size={14} /> Sign out</button>
      </header>
      {error ? <p className="identity-error" role="alert">{error}</p> : null}
      <div className="identity-content">{children}</div>
    </div>
  );
}
