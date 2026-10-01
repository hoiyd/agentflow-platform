import { cleanup, render, screen, waitFor, fireEvent } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { IdentityBoundary } from "./IdentityBoundary";

vi.mock("next/navigation", () => ({ usePathname: () => "/workspace" }));

afterEach(() => { cleanup(); vi.unstubAllGlobals(); sessionStorage.clear(); });

function session(body: unknown, status = 200) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(body), { status })));
}

it("does not mount business consumers before authentication resolves", async () => {
  let resolve!: (response: Response) => void;
  vi.stubGlobal("fetch", () => new Promise<Response>(next => { resolve = next; }));
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(screen.queryByText("Business content")).toBeNull();
  resolve(new Response(JSON.stringify({ mode: "oidc", authenticated: false, user: null, workspaces: [] })));
  expect(await screen.findByRole("link", { name: "Sign in" })).toBeTruthy();
  expect(screen.queryByText("Business content")).toBeNull();
});

it("preserves trusted-local use without an identity toolbar", async () => {
  session({ mode: "local", authenticated: false, user: null, workspaces: [] });
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(await screen.findByText("Business content")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Sign out" })).toBeNull();
});

it("keeps authenticated nonmembers outside the workbench", async () => {
  session({ mode: "oidc", authenticated: true, user: { id: "u", subject: "s", name: "Operator" }, workspaces: [] });
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(await screen.findByRole("heading", { name: "Workspace access required" })).toBeTruthy();
  expect(screen.queryByText("Business content")).toBeNull();
});

it("surfaces storage errors without silently enabling local mode", async () => {
  session({ error: "Storage unavailable" }, 503);
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect((await screen.findByRole("alert")).textContent).toContain("503");
  expect(screen.queryByText("Business content")).toBeNull();
});

it("invalidates mounted content on authentication expiry", async () => {
  session({ mode: "oidc", authenticated: true, user: { id: "u", subject: "s", name: "Operator" }, workspaces: ["a", "b"] });
  render(<IdentityBoundary><p>Business content</p></IdentityBoundary>);
  expect(await screen.findByText("Business content")).toBeTruthy();
  expect(screen.getByRole("combobox", { name: "Workspace" })).toBeTruthy();
  fireEvent(window, new Event("agentflow-auth-required"));
  await waitFor(() => expect(screen.queryByText("Business content")).toBeNull());
  expect(screen.getByRole("link", { name: "Sign in" })).toBeTruthy();
});
