import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { OperatorAttentionItem } from "../../lib/api";
import { OperatorAttentionPanel } from "./OperatorAttentionPanel";

const listRunAttention = vi.hoisted(() => vi.fn());

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  listRunAttention
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("shows derived evidence and keeps confirmed items visible while refreshing", async () => {
  listRunAttention.mockResolvedValueOnce([attentionFixture()]);
  render(<OperatorAttentionPanel />);

  expect(screen.getByText("Loading attention queue...")).toBeTruthy();
  expect(await screen.findByRole("heading", { name: "Run can be resumed" })).toBeTruthy();
  expect(screen.getByText("Observed at event 7")).toBeTruthy();

  listRunAttention.mockReturnValueOnce(new Promise(() => undefined));
  fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

  expect(screen.getByRole("heading", { name: "Run can be resumed" })).toBeTruthy();
  expect(screen.queryByText("No runs need operator attention.")).toBeNull();
});

function attentionFixture(): OperatorAttentionItem {
  return {
    run_id: "run-1",
    conversation_id: "conversation-1",
    conversation_title: "Repair deployment",
    run_status: "failed_recoverable",
    reason: "recovery_available",
    title: "Run can be resumed",
    message: "The run stopped unexpectedly and has a durable recovery point.",
    evidence: [{ kind: "run_error", id: "run-1", status: "failed_recoverable", summary: "worker interrupted" }],
    recommended_action: { kind: "resume_run", label: "Resume run", enabled: true, target_id: "run-1" },
    observation_sequence: 7,
    updated_at: "2026-09-20T08:00:00Z"
  };
}
