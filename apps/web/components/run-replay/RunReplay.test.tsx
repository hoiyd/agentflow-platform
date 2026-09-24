import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { RunReplay as RunReplayData } from "../../lib/api";
import { EventDetail, stepDuration } from "./RunEventDetails";
import { RunReplay } from "./RunReplay";

const getReplayPageData = vi.hoisted(() => vi.fn());

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("../../lib/replay-page-data", () => ({ getReplayPageData }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("does not double-count physical attempt latency in the step total", () => {
  const events: RunReplayData["run_events"] = [
    { id: "model-1", schema_version: 1, sequence: 1, run_id: "run-1", type: "model.completed", stage_id: "stage-1", timestamp: "2026-09-10T00:00:00Z", payload: { duration_ms: 120 } },
    { id: "attempt-1", schema_version: 1, sequence: 2, run_id: "run-1", type: "model.attempt_finished", stage_id: "stage-1", timestamp: "2026-09-10T00:00:01Z", payload: { duration_ms: 95 } }
  ];
  expect(stepDuration(events, "stage-1")).toBe(120);
});

it("shows failed attempt diagnostics without inventing token usage", () => {
  const event: RunReplayData["run_events"][number] = {
    id: "attempt-1",
    schema_version: 1,
    sequence: 1,
    run_id: "run-1",
    type: "model.attempt_finished",
    timestamp: "2026-09-10T00:00:00Z",
    payload: { attempt: 2, status: "failed", duration_ms: 80, time_to_first_token_ms: 12, error_kind: "invalid_response", usage_available: false }
  };
  render(<EventDetail event={event} />);
  expect(screen.getByText("First token")).toBeTruthy();
  expect(screen.getByText("invalid_response")).toBeTruthy();
  expect(screen.queryByText("Prompt")).toBeNull();
});

it("distinguishes truncated generation and a missing provider finish reason", () => {
  const base: RunReplayData["run_events"][number] = {
    id: "attempt-1", schema_version: 1, sequence: 1, run_id: "run-1",
    type: "model.attempt_finished", timestamp: "2026-09-10T00:00:00Z",
    payload: { attempt: 1, status: "failed", finish_reason: "length", error_kind: "incomplete_output" }
  };
  const view = render(<EventDetail event={base} />);
  expect(screen.getByText("Generation finish")).toBeTruthy();
  expect(screen.getByText("length")).toBeTruthy();
  expect(screen.getByText("incomplete_output")).toBeTruthy();

  view.rerender(<EventDetail event={{ ...base, payload: { attempt: 2, status: "completed", finish_reason: "missing" } }} />);
  expect(screen.getByText("Unconfirmed (not provided)")).toBeTruthy();
});

it("shows the effective sampling parameters recorded for a model request", () => {
  const event: RunReplayData["run_events"][number] = {
    id: "request-1", schema_version: 1, sequence: 1, run_id: "run-1",
    type: "model.request_prepared", timestamp: "2026-09-10T00:00:00Z",
    payload: { attempt: 1, operation: "chat.completion", parameters: { temperature: 0, top_p: 0.8, seed: 42 } }
  };
  render(<EventDetail event={event} />);
  expect(screen.getByText("Sampling")).toBeTruthy();
  expect(screen.getByText("Temperature 0 · Top-p 0.8 · Seed 42")).toBeTruthy();
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

it("keeps run comparison behind the replay evaluation menu", async () => {
  getReplayPageData.mockResolvedValue({
    data: replayFixture(),
    report: null,
    reportError: ""
  });

  render(<RunReplay runId="run-1" />);

  const menuLabel = await screen.findByText("Evaluation");
  const menu = menuLabel.closest("details");
  expect(menu?.hasAttribute("open")).toBe(false);

  fireEvent.click(menuLabel);
  expect(screen.getByRole("link", { name: "Compare with another run" }).getAttribute("href")).toBe(
    "/evaluations/compare?run=run-1&conversation=conversation-1"
  );
});

it("returns to the owning conversation instead of the landing page", async () => {
  getReplayPageData.mockResolvedValue({
    data: replayFixture(),
    report: null,
    reportError: ""
  });

  render(<RunReplay runId="run-1" />);

  await screen.findByRole("heading", { name: "Run replay" });
  const backLink = screen.getByRole("link", { name: "Back to chat" });
  expect(backLink.getAttribute("href")).toBe("/workspace?conversation=conversation-1");
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
  };
}
