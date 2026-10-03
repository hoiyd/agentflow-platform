import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { DEFAULT_COMPLETION_VERIFICATION } from "../../lib/verification";
import { TrustedHostCommands } from "../identity/WorkspaceContext";
import { VerifierConfigEditor } from "./VerifierConfigEditor";

afterEach(cleanup);

it("keeps host execution unavailable without a trusted local session", () => {
  render(<VerifierConfigEditor disabled={false} draft={DEFAULT_COMPLETION_VERIFICATION} onChange={vi.fn()} type="command" />);
  expect(screen.getByLabelText("Use command check").hasAttribute("disabled")).toBe(true);
  expect(screen.getByText(/requires an isolated runner/)).toBeTruthy();
});

it("retains explicit command configuration for the local operator", () => {
  render(<TrustedHostCommands.Provider value={true}><VerifierConfigEditor disabled={false} draft={DEFAULT_COMPLETION_VERIFICATION} onChange={vi.fn()} type="command" /></TrustedHostCommands.Provider>);
  expect(screen.getByLabelText("Use command check").hasAttribute("disabled")).toBe(false);
  expect(screen.getByText(/not a sandbox/)).toBeTruthy();
});
