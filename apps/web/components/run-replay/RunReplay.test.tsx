import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { RunReplay as RunReplayData } from "../../lib/api";
import { EventDetail, stepDuration } from "./RunEventDetails";
import { RunReplay } from "./RunReplay";

const getReplayPageData = vi.hoisted(() => vi.fn());
const resumeRun = vi.hoisted(() => vi.fn());
const reconciliation = vi.hoisted(() => ({ refresh: null as null | (() => Promise<void>) }));
vi.mock("../../lib/api", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api")>(), resumeRun
}));

vi.mock("next/navigation", () => ({ useRouter: () => ({ push: vi.fn() }) }));
vi.mock("../../lib/replay-page-data", () => ({ getReplayPageData }));
vi.mock("./RecoveryActions", async (importOriginal) => ({
  ...await importOriginal<typeof import("./RecoveryActions")>(),
  ToolEffectReconciliationPanel: ({ onChanged }: { onChanged: () => Promise<void> }) => {
    reconciliation.refresh = onChanged;
    return <button onClick={() => void onChanged()}>Refresh reconciled replay</button>;
  }
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  resumeRun.mockReset();
});

function recoverableFixture() {
  const data = replayFixture();
  data.run.status = "failed_recoverable";
  data.recovery_summary = {
    reason: "worker_lost",
    title: "Run can be resumed", message: "Saved evidence remains available.",
    evidence: [{ kind: "checkpoint", summary: "Durable checkpoint" }], artifact_refs: [],
    actions: [{ kind: "resume_run", label: "Resume saved run", enabled: true }]
  };
  return data;
}

it("keeps replay evidence and original status when Resume is rejected before acceptance", async () => {
  getReplayPageData.mockResolvedValue({ data: recoverableFixture(), report: null, reportError: "" });
  resumeRun.mockRejectedValue(new Error("Resume denied"));
  render(<RunReplay runId="run-1" />);
  fireEvent.click(await screen.findByRole("button", { name: "Resume saved run" }));
  await screen.findByText("Resume denied");
  expect(screen.getByRole("heading", { name: "Run replay" })).toBeTruthy();
  expect(screen.getByText("Durable checkpoint")).toBeTruthy();
  expect(screen.getByText("failed_recoverable")).toBeTruthy();
});

it("keeps accepted server status and evidence after a Resume transport failure", async () => {
  getReplayPageData.mockResolvedValue({ data: recoverableFixture(), report: null, reportError: "" });
  resumeRun.mockImplementation(async (_input, emit) => {
    emit({ type: "run_state", run_id: "run-1", conversation_id: "conversation-1", status: "running" });
    throw new Error("Resume connection lost");
  });
  render(<RunReplay runId="run-1" />);
  fireEvent.click(await screen.findByRole("button", { name: "Resume saved run" }));
  await screen.findByText("Resume connection lost");
  expect(screen.getByText("running")).toBeTruthy();
  expect(screen.getByText("Durable checkpoint")).toBeTruthy();
  expect((screen.getByRole("button", { name: "Resume saved run" }) as HTMLButtonElement).disabled).toBe(true);
});

it("preserves accepted evidence if the post-Resume refresh fails", async () => {
  getReplayPageData.mockResolvedValueOnce({ data: recoverableFixture(), report: null, reportError: "" })
    .mockRejectedValueOnce(new Error("refresh unavailable"));
  resumeRun.mockImplementation(async (_input, emit) => emit({ type: "done", run_id: "run-1", status: "completed" }));
  render(<RunReplay runId="run-1" />);
  fireEvent.click(await screen.findByRole("button", { name: "Resume saved run" }));
  await screen.findByText(/refresh unavailable/);
  expect(screen.getByRole("heading", { name: "Run replay" })).toBeTruthy();
  expect(screen.getByText("completed")).toBeTruthy();
  expect(screen.getByText("Durable checkpoint")).toBeTruthy();
});

it("ignores old Resume events and refreshes after navigating to a different Run", async () => {
  let emit!: (event: unknown) => void;
  let finish!: () => void;
  resumeRun.mockImplementation((_input, onEvent) => new Promise<void>(resolve => { emit = onEvent; finish = resolve; }));
  const next = replayFixture();
  next.run = { ...next.run, id: "run-2" };
  next.conversation = { ...next.conversation, title: "Second run" };
  getReplayPageData.mockImplementation(async (id) => ({ data: id === "run-1" ? recoverableFixture() : next, report: null, reportError: "" }));
  const view = render(<RunReplay runId="run-1" />);
  fireEvent.click(await screen.findByRole("button", { name: "Resume saved run" }));
  await waitFor(() => expect(resumeRun).toHaveBeenCalledTimes(1));
  view.rerender(<RunReplay runId="run-2" />);
  await screen.findByText("Second run");
  await act(async () => { emit({ type: "done", run_id: "run-1", status: "failed" }); finish(); });
  expect(screen.getByText("Second run")).toBeTruthy();
  expect(screen.getByText("completed")).toBeTruthy();
  expect(getReplayPageData.mock.calls.map(call => call[0])).toEqual(["run-1", "run-2"]);
});

it("admits only one Resume before React flushes repeated clicks", async () => {
  getReplayPageData.mockResolvedValue({ data: recoverableFixture(), report: null, reportError: "" });
  resumeRun.mockImplementation(() => new Promise(() => {}));
  render(<RunReplay runId="run-1" />);
  const button = await screen.findByRole("button", { name: "Resume saved run" });
  act(() => { button.click(); button.click(); });
  expect(resumeRun).toHaveBeenCalledTimes(1);
  expect((screen.getByRole("button", { name: "Resuming..." }) as HTMLButtonElement).disabled).toBe(true);
});

function reconciliationFixture() {
  const data = recoverableFixture();
  data.recovery_summary!.actions.push({ kind: "reconcile_tool_effect", label: "Review effects", enabled: true });
  return data;
}

it("keeps the current event selection and evidence when reconciliation refresh fails", async () => {
  const data = reconciliationFixture();
  data.run_events = [1, 2].map(sequence => ({
    id: `event-${sequence}`, sequence, schema_version: 1, run_id: "run-1",
    type: sequence === 1 ? "run.started" : "run.failed", timestamp: "2026-09-10T00:00:00Z", payload: {}
  }));
  getReplayPageData.mockResolvedValueOnce({ data, report: null, reportError: "" })
    .mockRejectedValueOnce(new Error("read after reconciliation failed"));
  render(<RunReplay runId="run-1" />);
  const selected = await screen.findByRole("button", { name: /run.failed/ });
  fireEvent.click(selected);
  fireEvent.click(screen.getByRole("button", { name: "Refresh reconciled replay" }));
  await screen.findByText(/Replay refresh unavailable: read after reconciliation failed/);
  expect(selected.className).toContain("active");
  expect(screen.getByText("Durable checkpoint")).toBeTruthy();
});

it("accepts only the latest refresh even when the earlier read ignores abort", async () => {
  getReplayPageData.mockResolvedValueOnce({ data: reconciliationFixture(), report: null, reportError: "" });
  render(<RunReplay runId="run-1" />);
  await screen.findByRole("button", { name: "Refresh reconciled replay" });
  let finishOld!: (page: unknown) => void;
  const newer = reconciliationFixture();
  newer.conversation.title = "Latest evidence";
  getReplayPageData.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }))
    .mockResolvedValueOnce({ data: newer, report: null, reportError: "" });
  const refresh = reconciliation.refresh!;
  await act(async () => { void refresh(); await refresh(); });
  await screen.findByText("Latest evidence");
  expect((getReplayPageData.mock.calls[1][1] as AbortSignal).aborted).toBe(true);
  await act(async () => finishOld({ data: reconciliationFixture(), report: null, reportError: "old warning" }));
  expect(screen.getByText("Latest evidence")).toBeTruthy();
  expect(screen.queryByText(/old warning/)).toBeNull();
});

it("does not let an older read roll back a newly accepted Resume event", async () => {
  getReplayPageData.mockResolvedValueOnce({ data: reconciliationFixture(), report: null, reportError: "" });
  render(<RunReplay runId="run-1" />);
  await screen.findByRole("button", { name: "Refresh reconciled replay" });
  let finishOld!: (page: unknown) => void;
  getReplayPageData.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve; }));
  fireEvent.click(screen.getByRole("button", { name: "Refresh reconciled replay" }));
  resumeRun.mockImplementation(async (_input, emit) => {
    emit({ type: "done", run_id: "run-1", status: "completed" });
    throw new Error("late transport failure");
  });
  fireEvent.click(screen.getByRole("button", { name: "Resume saved run" }));
  await screen.findByText("late transport failure");
  await act(async () => finishOld({ data: reconciliationFixture(), report: null, reportError: "" }));
  expect(screen.getByText("completed")).toBeTruthy();
});

it("shows a fatal error only when no Replay has been loaded", async () => {
  getReplayPageData.mockRejectedValueOnce(new Error("initial replay unavailable"));
  render(<RunReplay runId="run-1" />);
  await screen.findByText("initial replay unavailable");
  expect(screen.queryByRole("heading", { name: "Run replay" })).toBeNull();
});

it("rejects a mismatched read without displaying another Run's evidence", async () => {
  getReplayPageData.mockResolvedValueOnce({ data: replayFixture(), report: null, reportError: "" });
  render(<RunReplay runId="run-other" />);
  await screen.findByText("Replay response belongs to another Run");
  expect(screen.queryByRole("heading", { name: "Run replay" })).toBeNull();
});

it("ignores an old initial-load rejection after switching Run identity", async () => {
  let failOld!: (error: Error) => void;
  const next = replayFixture();
  next.run.id = "run-2";
  next.conversation.title = "Second run";
  getReplayPageData.mockImplementationOnce(() => new Promise((_resolve, reject) => { failOld = reject; }))
    .mockResolvedValueOnce({ data: next, report: null, reportError: "" });
  const view = render(<RunReplay runId="run-1" />);
  await waitFor(() => expect(getReplayPageData).toHaveBeenCalledTimes(1));
  view.rerender(<RunReplay runId="run-2" />);
  await screen.findByText("Second run");
  await act(async () => failOld(new Error("old initial read failed")));
  expect(screen.queryByText("old initial read failed")).toBeNull();
  expect((getReplayPageData.mock.calls[0][1] as AbortSignal).aborted).toBe(true);
});

it("does not start a new read from a reconciliation callback after unmount", async () => {
  getReplayPageData.mockResolvedValue({ data: reconciliationFixture(), report: null, reportError: "" });
  const view = render(<RunReplay runId="run-1" />);
  await screen.findByRole("button", { name: "Refresh reconciled replay" });
  const oldRefresh = reconciliation.refresh!;
  view.unmount();
  await act(async () => oldRefresh());
  expect(getReplayPageData).toHaveBeenCalledTimes(1);
});

it("shows frozen skill identity and resource read details in tool events", () => {
  const event: RunReplayData["run_events"][number] = {
    id: "skill-read", schema_version: 1, sequence: 3, run_id: "run-1",
    type: "tool.completed", timestamp: "2026-09-10T00:00:00Z",
    payload: { tool_name: "skill_read", result: { name: "knowledge-answer", hash: "sha256-method", path: "references/checklist.md", offset: 0, next_offset: 32, total_bytes: 64, truncated: true } }
  };
  render(<EventDetail event={event} />);
  expect(screen.getByText("Skill")).toBeTruthy();
  expect(screen.getByText("knowledge-answer")).toBeTruthy();
  expect(screen.getByText("sha256-method")).toBeTruthy();
  expect(screen.getByText("references/checklist.md")).toBeTruthy();
  expect(screen.getByText("0–32 / 64 bytes")).toBeTruthy();
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
  expect(screen.getByText("Attempt first token")).toBeTruthy();
  expect(screen.getByText("invalid_response")).toBeTruthy();
  expect(screen.queryByText("Prompt")).toBeNull();
});

it("separates local admission waits from HTTP and stream timing", () => {
  const event: RunReplayData["run_events"][number] = {
    id: "attempt-2", schema_version: 1, sequence: 2, run_id: "run-1",
    type: "model.attempt_finished", timestamp: "2026-09-10T00:00:00Z",
    payload: { attempt: 1, status: "completed", duration_ms: 80, rate_limit_wait_ms: 12,
      model_permit_wait_ms: 25, owner_capacity_wait_ms: 18, http_duration_ms: 42, http_time_to_first_token_ms: 10 }
  };
  render(<EventDetail event={event} />);
  expect(screen.getByText("Local rate wait")).toBeTruthy();
  expect(screen.getByText("Model permit wait")).toBeTruthy();
  expect(screen.getByText("Owner capacity wait")).toBeTruthy();
  expect(screen.getByText("HTTP + stream")).toBeTruthy();
  expect(screen.getByText("HTTP first token")).toBeTruthy();
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

it("identifies simulated model events in replay", () => {
  const event: RunReplayData["run_events"][number] = {
    id: "simulated-1", schema_version: 1, sequence: 1, run_id: "run-1",
    type: "model.started", timestamp: "2026-09-10T00:00:00Z",
    payload: { model: "local_fallback", provider: "simulated", simulated: true }
  };
  render(<EventDetail event={event} />);
  expect(screen.getByText("Offline simulation")).toBeTruthy();
});

it("keeps retrieval metadata and source content in event details", () => {
  const event: RunReplayData["run_events"][number] = {
    id: "retrieval-1", schema_version: 1, sequence: 1, run_id: "run-1",
    type: "retrieval.completed", timestamp: "2026-09-10T00:00:00Z",
    payload: {
      fusion: { algorithm: "rrf", version: "v1", rank_constant: 60 },
      retrieved_chunks: [{ source_id: "S1", document_title: "Architecture", content: "Relevant passage", score: 0.9 }]
    }
  };
  render(<EventDetail event={event} />);
  expect(screen.getByText("Fusion")).toBeTruthy();
  expect(screen.getByText("Architecture")).toBeTruthy();
  expect(screen.getByText("Relevant passage")).toBeTruthy();
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

it("shows current Run Web sources and opens their original Tool event", async () => {
  const data = replayFixture();
  data.run_events = [{
    id: "tool-event-1", schema_version: 1, sequence: 1, run_id: "run-1", type: "tool.completed",
    timestamp: "2026-09-10T00:00:00Z", payload: { tool_name: "web_search" }
  }];
  data.messages = [{
    id: "message-1", conversation_id: "conversation-1", role: "assistant", content: "Answer [W1]",
    created_at: "2026-09-10T00:00:00Z", web_citations: [{
      source_id: "W1", title: "Official page", url: "https://example.com/", run_id: "run-1",
      tool_call_id: "call-1", tool_event_id: "tool-event-1"
    }]
  }];
  getReplayPageData.mockResolvedValue({ data, report: null, reportError: "" });
  render(<RunReplay initialEventId="tool-event-1" runId="run-1" />);

  expect(await screen.findByRole("link", { name: "Official page" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "Tool event" }).getAttribute("href"))
    .toBe("/runs/run-1?event=tool-event-1#run-event-detail");
  expect(screen.getByRole("button", { name: /tool.completed/ }).className).toContain("active");
  expect(screen.getByText(/"tool_name": "web_search"/)).toBeTruthy();
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
      partial_outputs: [],
      tool_progress: [],
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
