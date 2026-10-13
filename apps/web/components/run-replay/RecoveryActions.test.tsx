import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ToolEffect } from "../../lib/api";
import { ToolEffectReconciliationPanel } from "./RecoveryActions";

const api = vi.hoisted(() => ({ list: vi.fn(), reconcile: vi.fn() }));
vi.mock("../../lib/api", () => ({ listToolEffects: api.list, reconcileToolEffect: api.reconcile }));
vi.mock("../identity/WorkspaceContext", () => ({ useWorkspaceReadOnly: () => false }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it.each(["file_write", "file_patch", "other_writer"])("names %s recovery according to its actual effect", async tool => {
  const effect: ToolEffect = { idempotency_key: "effect", version: 3, run_id: "run", stage_id: "", turn_id: "turn", tool_call_id: "call", tool_name: tool, request_hash: "hash", status: "needs_reconciliation", has_result: true, created_at: "2026-10-13T00:00:00Z", updated_at: "2026-10-13T00:00:00Z", available_actions: ["retry_with_same_key"] };
  api.list.mockResolvedValue([effect]);
  render(<ToolEffectReconciliationPanel runId="run" onChanged={vi.fn()} />);
  expect(await screen.findByRole("option", { name: tool === "other_writer" ? "Retry with same key" : "Verify saved file" })).toBeTruthy();
});
