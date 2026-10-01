import { cleanup, render, screen, waitFor, fireEvent } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { IdentityBoundary } from "./IdentityBoundary";

vi.mock("next/navigation", () => ({ usePathname: () => "/workspace" }));

const redirect = vi.fn();
beforeEach(() => { redirect.mockReset(); vi.stubGlobal("location", { replace: redirect }); });

afterEach(() => { cleanup(); vi.unstubAllGlobals(); sessionStorage.clear(); });

function session(body: unknown, status = 200) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status })));
}

it("does not mount business consumers before authentication resolves", async () => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal("fetch", () => new Promise<Response>(next => { resolve = next; }));
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(screen.queryByText("Business content")).toBeNull();
  expect(redirect).not.toHaveBeenCalled();
  resolve(new Response(JSON.stringify({ mode: "oidc", authenticated: false, user: null, workspaces: [] })));
  await waitFor(() => expect(redirect).toHaveBeenCalledWith(expect.stringContaining("/api/auth/login")));
  expect(redirect).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("heading", { name: "Sign in to AgentFlow" })).toBeNull();
  expect(screen.queryByRole("link", { name: "Create account" })).toBeNull();
  expect(screen.queryByText("Business content")).toBeNull();
});

it("preserves trusted-local use without an identity toolbar", async () => {
  session({ mode: "local", authenticated: false, user: null, workspaces: [] });
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(await screen.findByText("Business content")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Sign out" })).toBeNull();
  expect(redirect).not.toHaveBeenCalled();
});

it("keeps authenticated nonmembers outside the workbench", async () => {
  session({ mode: "oidc", authenticated: true, user: { id: "u", subject: "s", name: "Operator" }, workspaces: [] });
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(await screen.findByRole("heading", { name: "Workspace access required" })).toBeTruthy();
  expect(screen.queryByText("Business content")).toBeNull();
  expect(redirect).not.toHaveBeenCalled();
});

it("surfaces storage errors without silently enabling local mode", async () => {
  session({ error: "Storage unavailable" }, 503);
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect((await screen.findByRole("alert")).textContent).toContain("503");
  expect(screen.queryByText("Business content")).toBeNull();
  expect(redirect).not.toHaveBeenCalled();
});

it("invalidates mounted content on authentication expiry", async () => {
  session({ mode: "oidc", authenticated: true, user: { id: "u", subject: "s", name: "Operator" }, workspaces: ["a", "b"] });
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(await screen.findByText("Business content")).toBeTruthy();
  expect(screen.getByRole("button", { name: "Workspace: a" })).toBeTruthy();
  fireEvent(window, new Event("agentflow-auth-required"));
  await waitFor(() => expect(screen.queryByText("Business content")).toBeNull());
  await waitFor(() => expect(redirect).toHaveBeenCalledTimes(1));
  fireEvent(window, new Event("agentflow-auth-required"));
  await waitFor(() => expect(redirect).toHaveBeenCalledTimes(1));
});
