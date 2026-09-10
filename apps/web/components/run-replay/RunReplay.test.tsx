import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { RunReplay as RunReplayData } from "../../lib/api";
import { RunReplay } from "./RunReplay";

const getReplayPageData = vi.hoisted(() => vi.fn());

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("../../lib/replay-page-data", () => ({ getReplayPageData }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("keeps replay available when the episode report fails", async () => {
  getReplayPageData.mockResolvedValue({
    data: replayFixture(),
    report: null,
    reportError: "report unavailable"
  });

  render(<RunReplay runId="run-1" />);

  expect(await screen.findByRole("heading", { name: "Run replay" })).toBeTruthy();
  expect(screen.getByRole("status").textContent).toContain("Episode report unavailable: report unavailable");
});

function replayFixture(): RunReplayData {
  const timestamp = "2026-09-10T00:00:00Z";
  const totals = {
    model_calls: 0,
    tool_calls: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    total_tokens: 0,
    estimated_cost_micros: 0,
    open_reservations: 0
  };
  const summary = {
    run_id: "run-1",
    status: "completed" as const,
    total_duration_ms: 0,
    total_tokens: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    token_usage_estimated: false,
    llm_calls: 0,
    tool_calls: 0,
    error_count: 0
  };
  const ledger = { run_id: "run-1", budget: {}, totals, entries: [] };
  return {
    run: {
      id: "run-1",
      workspace_id: "default_workspace",
      agent_id: "agent-1",
      conversation_id: "conversation-1",
      status: "completed",
      verification_status: "not_required",
      active_runtime_ms: 0,
      created_at: timestamp,
      updated_at: timestamp
    },
    projection: {
      run: {
        run_id: "run-1",
        conversation_id: "conversation-1",
        status: "completed",
        verification_status: "not_required",
        active_stage_ids: [],
        active_turn_ids: [],
        active_model_call_ids: [],
        active_tool_call_ids: [],
        summary,
        as_of_sequence: 0
      },
      usage: { ledger, as_of_sequence: 0 },
      verification: {
        status: "not_required",
        latest_attempt: 0,
        evidence_count: 0,
        fresh_evidence_count: 0,
        as_of_sequence: 0
      },
      as_of_sequence: 0,
      invariant_failures: []
    },
    conversation: {
      id: "conversation-1",
      workspace_id: "default_workspace",
      title: "Replay fixture",
      created_at: timestamp,
      updated_at: timestamp
    },
    messages: [],
    steps: [],
    summary,
    usage_ledger: ledger,
    run_events: [],
    stage_checkpoints: [],
    tool_effects: [],
    tool_artifacts: [],
    verification_evidence: [],
    verification_artifacts: [],
    task_state_revisions: [],
    child_delegations: []
  };
}
