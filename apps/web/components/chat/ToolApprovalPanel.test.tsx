import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ToolApproval } from "../../lib/tool-approval-api";
import { ToolApprovalPanel } from "./ToolApprovalPanel";

afterEach(cleanup);

const approval: ToolApproval = {
  id: "approval-1", run_id: "run-1", conversation_id: "conversation-1", workspace_id: "1", owner_id: "owner-1",
  turn_id: "turn-1", tool_call_id: "call-1", tool: "calculator", arguments: { expression: "21 * 2" },
  arguments_hash: "args", definition_revision: "definition", policy_hash: "policy", snapshot_hash: "snapshot",
  version: 1, status: "pending", created_at: "2026-11-14T00:00:00Z", expires_at: "2026-11-14T00:30:00Z"
};

// Failure inventory: non-file parameters must be visible; structured/empty
// arguments must not render misleading file placeholders; busy cannot approve.
it("shows every argument without assuming a filesystem operation", () => {
  const decide = vi.fn();
  render(<ToolApprovalPanel items={[approval]} onDecision={decide} />);
  expect(screen.getByText("expression")).toBeTruthy();
  expect(screen.getByText("21 * 2")).toBeTruthy();
  expect(screen.queryByText("Replace")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Approve once & continue" }));
  expect(decide).toHaveBeenCalledWith(approval, "approve");
});

it("renders nested values and blocks decisions while busy", () => {
  const decide = vi.fn();
  render(<ToolApprovalPanel items={[{ ...approval, tool: "custom_action", arguments: { payload: { dry_run: false, targets: ["example"] } } }]} busy onDecision={decide} />);
  expect(screen.getByText("payload")).toBeTruthy();
  expect(screen.getByText(/"dry_run": false/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Approve once & continue" }));
  expect(decide).not.toHaveBeenCalled();
});

it("represents a zero-argument call explicitly", () => {
  render(<ToolApprovalPanel items={[{ ...approval, arguments: {} }]} />);
  expect(screen.getByText("No arguments")).toBeTruthy();
});
