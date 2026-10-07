import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ChatShell } from "./ChatShell";

const api = vi.hoisted(() => ({
  getAPIHealth: vi.fn(),
  listConversations: vi.fn(),
  listMessages: vi.fn(),
  getTaskState: vi.fn(),
  getRunProjection: vi.fn(),
  listRuns: vi.fn(),
  listCollaborationSteps: vi.fn(),
  listAgents: vi.fn(),
  listTools: vi.fn(),
  listSkills: vi.fn(),
  streamChat: vi.fn(),
  continueRun: vi.fn(),
  resumeRun: vi.fn(),
  cancelRun: vi.fn(),
  observeRunEvents: vi.fn()
}));

vi.mock("../lib/api", () => api);
const knowledgeAPI = vi.hoisted(() => ({ listDocuments: vi.fn() }));
vi.mock("../lib/knowledge-api", () => knowledgeAPI);
vi.mock("./chat/ChatChrome", () => ({
  Sidebar: ({ onOpenConversation }: { onOpenConversation: (id: string) => void }) => (
    <button onClick={() => onOpenConversation("b")}>Open B</button>
  ),
  Topbar: () => null,
  ToolsPanel: () => null
}));
vi.mock("./chat/ChatWorkspace", () => ({
  ChatWorkspace: ({ messages, planDraft, runStatus, isStreaming, isCanceling, onContinue, onResume, onCancel }: {
    planDraft: string;
    messages: Array<{ content: string }>; runStatus: string; isStreaming: boolean; isCanceling: boolean;
    onContinue: () => void; onResume: (input?: string) => void; onCancel: () => void;
  }) => (
    <div>{messages.map((message, index) => <p data-testid="message" key={index}>{message.content}</p>)}<output>{runStatus}</output><output>{planDraft}</output>
      <output aria-label="Command state">{isStreaming ? "busy" : "idle"}</output>
      <output aria-label="Cancel state">{isCanceling ? "canceling" : "idle"}</output>
      <button onClick={() => onContinue()}>Continue</button><button onClick={() => onResume()}>Resume</button><button onClick={onCancel}>Cancel run</button>
      <button onClick={() => onResume("answer")}>Resume with input</button>
    </div>
  )
}));
let submitFromComposer: (event: React.FormEvent) => void;
vi.mock("./chat/ChatComposer", () => ({
  ChatComposer: ({ input, error, onInputChange, onSubmit }: {
    input: string; error: string; onInputChange: (value: string) => void; onSubmit: (event: React.FormEvent) => void;
  }) => {
    submitFromComposer = onSubmit;
    return <form onSubmit={onSubmit}><input aria-label="Prompt" onChange={(event) => onInputChange(event.target.value)} value={input} /><button>Send</button><output aria-label="Error">{error}</output></form>;
  }
}));
vi.mock("./chat/ChatDialogs", () => ({ ChatDialogs: () => null }));

afterEach(cleanup);

it("opens Knowledge when returning from retrieval evaluation", async () => {
  setupAPI();
  render(<ChatShell initialView="knowledge" />);

  expect(await screen.findByRole("heading", { name: "Knowledge" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "Evaluate index" })).toBeTruthy();
});

it("keeps the Run and collaboration trace when partial-output recovery is unavailable", async () => {
  setupAPI();
  api.listRuns.mockResolvedValue([{ id: "stopped", conversation_id: "a", status: "failed_recoverable" }]);
  api.listCollaborationSteps.mockResolvedValue([{ id: "stage", role: "planner", status: "completed", output: "saved plan" }]);
  api.getRunProjection.mockRejectedValue(new Error("projection unavailable"));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("Partial output recovery unavailable: projection unavailable");
  expect(screen.getByText("failed_recoverable")).toBeTruthy();
  expect(screen.getByText("saved plan")).toBeTruthy();
  expect(screen.getByText("A baseline")).toBeTruthy();
});

it("does not apply a previous conversation's stream events after navigation", async () => {
  setupAPI();
  let emit!: (event: { type: string; delta?: string }) => void;
  let finish!: () => void;
  api.streamChat.mockImplementation((_input, onEvent) => new Promise<void>((resolve) => {
    emit = onEvent;
    finish = resolve;
  }));
  render(<ChatShell initialConversationId="a" />);
  expect(await screen.findByText("A baseline")).toBeTruthy();

  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(api.streamChat).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Open B" }));
  expect(await screen.findByText("B baseline")).toBeTruthy();

  await act(async () => { emit({ type: "model_delta", delta: "old reply" }); finish(); });
  expect(screen.getByText("B baseline")).toBeTruthy();
  expect(screen.queryByText("old reply")).toBeNull();
});

it("restores the prompt and removes optimistic messages when a stream fails before any event", async () => {
  setupAPI();
  api.listRuns.mockResolvedValue([{ id: "run_old", conversation_id: "a", agent_id: "", status: "completed", verification_status: "not_required" }]);
  api.streamChat.mockRejectedValue(new Error("unavailable"));
  render(<ChatShell initialConversationId="a" />);
  expect(await screen.findByText("A baseline")).toBeTruthy();
  expect(await screen.findByText("completed")).toBeTruthy();

  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(screen.getByRole("textbox", { name: "Prompt" }).getAttribute("value")).toBe("question"));
  expect(screen.getByText("A baseline")).toBeTruthy();
  expect(screen.getByText("completed")).toBeTruthy();
  expect(screen.queryByText("question")).toBeNull();
});

function setupAPI() {
  vi.resetAllMocks();
  api.getAPIHealth.mockResolvedValue({ status: "ok" });
  api.listConversations.mockResolvedValue([
    { id: "a", workspace_id: "default_workspace", title: "A" },
    { id: "b", workspace_id: "default_workspace", title: "B" }
  ]);
  api.listMessages.mockImplementation((id: string) => Promise.resolve([{
    id: id + "-message", conversation_id: id, role: "assistant", content: id === "a" ? "A baseline" : "B baseline",
    created_at: "2026-09-01T00:00:00Z"
  }]));
  api.getTaskState.mockResolvedValue(null);
  api.getRunProjection.mockResolvedValue({ run: { run_id: "r" }, as_of_sequence: 0, partial_outputs: [] });
  api.listRuns.mockResolvedValue([]);
  api.listCollaborationSteps.mockResolvedValue([]);
  api.listAgents.mockResolvedValue([]);
  api.listTools.mockResolvedValue([]);
  api.listSkills.mockResolvedValue([]);
  api.observeRunEvents.mockImplementation(() => new Promise(() => {}));
  knowledgeAPI.listDocuments.mockResolvedValue([]);
}

it("accepts only one stream command before React rerenders", async () => {
  setupAPI();
  api.streamChat.mockImplementation(() => new Promise(() => {}));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "question" } });
  const submit = submitFromComposer;
  act(() => {
    submit({ preventDefault() {} } as React.FormEvent);
    submit({ preventDefault() {} } as React.FormEvent);
  });
  expect(api.streamChat).toHaveBeenCalledTimes(1);
  expect(screen.getAllByText("question")).toHaveLength(1);
});

it("does not roll back partial output or resubmit a consumed prompt on transport failure", async () => {
  setupAPI();
  let rejectStream!: (error: Error) => void;
  api.streamChat.mockImplementation((_input, emit) => {
    emit({ type: "run_state", run_id: "r", status: "running" });
    emit({ type: "model_delta", delta: "Partial reply" });
    return new Promise<void>((_resolve, reject) => { rejectStream = reject; });
  });
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await screen.findByText("Partial reply");
  // Observe both ownership phases explicitly instead of depending on whether
  // React batches an immediately rejected command into the same render.
  await waitFor(() => expect(api.observeRunEvents).toHaveBeenCalledWith("r", expect.anything()));
  const commandObserver = api.observeRunEvents.mock.calls.at(-1)![1];
  expect(commandObserver.signal.aborted).toBe(false);
  expect(screen.getByLabelText("Command state").textContent).toBe("busy");
  await act(async () => rejectStream(new Error("transport disconnected")));
  expect(screen.getByRole("textbox", { name: "Prompt" }).getAttribute("value")).toBe("");
  expect(screen.getByText("question")).toBeTruthy();
  await waitFor(() => {
    expect(commandObserver.signal.aborted).toBe(true);
    const recoveryObserver = api.observeRunEvents.mock.calls.at(-1)![1];
    expect(recoveryObserver).not.toBe(commandObserver);
    expect(recoveryObserver.signal.aborted).toBe(false);
    expect(api.observeRunEvents).toHaveBeenLastCalledWith("r", expect.anything());
  });
  act(() => {
    commandObserver.onEvent({ type: "model_delta", delta: "Late old reply" }, 99, false);
    commandObserver.onSnapshot({ run: { run_id: "r", conversation_id: "a", status: "completed" } });
  });
  expect(screen.queryByText("Late old reply")).toBeNull();
  expect(screen.getByText("Partial reply")).toBeTruthy();
  expect(screen.getByText("running")).toBeTruthy();
  expect(screen.getByLabelText("Command state").textContent).toBe("idle");
  expect(screen.getByLabelText("Error").textContent).toBe("transport disconnected");
  expect(api.streamChat).toHaveBeenCalledTimes(1);
});

for (const command of ["Continue", "Resume"] as const) {
  it(`${command} failure before the first event restores the waiting Run and removes its draft`, async () => {
    setupAPI();
    api.listRuns.mockResolvedValue([{ id: "waiting", conversation_id: "a", status: "waiting_for_user", verification_status: "pending" }]);
    api.listCollaborationSteps.mockResolvedValue([{ id: "stage", role: command === "Continue" ? "planner" : "human_input", status: command === "Continue" ? "completed" : "running", output: "approved plan" }]);
    const request = command === "Continue" ? api.continueRun : api.resumeRun;
    request.mockRejectedValue(new Error("request rejected"));
    render(<ChatShell initialConversationId="a" />);
    await screen.findByText("waiting_for_user");
    if (command === "Continue") await screen.findByText("approved plan");
    // Resume reads the supplied answer, not a fabricated Run/Stage identity.
    if (command === "Resume") {
      // The test workspace exposes the existing handler's optional answer.
      fireEvent.click(screen.getByRole("button", { name: "Resume with input" }));
    } else {
      fireEvent.click(screen.getByRole("button", { name: command }));
    }
    await waitFor(() => expect(request).toHaveBeenCalledTimes(1));
    await screen.findByText("request rejected");
    expect(screen.getByText("waiting_for_user")).toBeTruthy();
    expect(screen.getByLabelText("Command state").textContent).toBe("idle");
    expect(screen.getByText("A baseline")).toBeTruthy();
    expect(screen.getAllByTestId("message")).toHaveLength(1);
  });
}

it("ignores a late cancellation response after the stream has reached a terminal status", async () => {
  setupAPI();
  let emit!: (event: Record<string, unknown>) => void;
  let resolveCancel!: (value: Record<string, unknown>) => void;
  api.streamChat.mockImplementation((_input, onEvent) => new Promise(() => { emit = onEvent; }));
  api.cancelRun.mockImplementation(() => new Promise((resolve) => { resolveCancel = resolve; }));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  act(() => emit({ type: "run_state", run_id: "r", status: "running" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel run" }));
  await act(async () => {
    emit({ type: "run_state", run_id: "r", status: "completed" });
    resolveCancel({ id: "r", status: "canceling" });
  });
  expect(screen.getByText("completed")).toBeTruthy();
  expect(screen.getByLabelText("Cancel state").textContent).toBe("idle");
});

it("cancels local observers on navigation and ignores their late events and snapshots", async () => {
  setupAPI();
  api.listRuns.mockResolvedValue([{ id: "active", conversation_id: "a", status: "running" }]);
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.observeRunEvents).toHaveBeenCalledTimes(1));
  const observer = api.observeRunEvents.mock.calls[0][1];
  act(() => observer.onEvent({ type: "run_state", run_id: "active", status: "queued" }, 1, true));
  expect(screen.getByText("running")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Open B" }));
  await screen.findByText("B baseline");
  expect(observer.signal.aborted).toBe(true);
  act(() => {
    observer.onSnapshot({ run: { run_id: "active", conversation_id: "a", status: "completed" } });
    observer.onEvent({ type: "stage_state", role: "planner", output: "old plan" }, 2, false);
  });
  expect(screen.queryByText("completed")).toBeNull();
  expect(screen.queryByText("old plan")).toBeNull();
});

it("reloads persisted messages once when an observed Run stops and rejects mismatched snapshots", async () => {
  setupAPI();
  api.listRuns.mockResolvedValue([{ id: "active", conversation_id: "a", status: "running" }]);
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.observeRunEvents).toHaveBeenCalledTimes(1));
  const observer = api.observeRunEvents.mock.calls[0][1];
  act(() => observer.onSnapshot({ run: { run_id: "other", conversation_id: "b", status: "completed" } }));
  expect(screen.getByText("running")).toBeTruthy();
  api.listRuns.mockResolvedValue([{ id: "active", conversation_id: "a", status: "completed" }]);
  api.listMessages.mockResolvedValue([{ id: "final", conversation_id: "a", role: "assistant", content: "Durable answer" }]);
  const reloads = api.listMessages.mock.calls.length;
  act(() => {
    observer.onSnapshot({ run: { run_id: "active", conversation_id: "a", status: "completed" } });
    observer.onEvent({ type: "run_state", run_id: "active", status: "completed" }, 2, false);
  });
  await screen.findByText("Durable answer");
  expect(screen.getByText("completed")).toBeTruthy();
  expect(api.listMessages).toHaveBeenCalledTimes(reloads + 1);
});

it("does not apply cancellation errors to the newly selected conversation", async () => {
  setupAPI();
  let emit!: (event: Record<string, unknown>) => void;
  let rejectCancel!: (error: Error) => void;
  api.streamChat.mockImplementation((_input, onEvent) => new Promise(() => { emit = onEvent; }));
  api.cancelRun.mockImplementation(() => new Promise((_resolve, reject) => { rejectCancel = reject; }));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  act(() => emit({ type: "run_state", run_id: "r", status: "running" }));
  fireEvent.click(screen.getByRole("button", { name: "Cancel run" }));
  fireEvent.click(screen.getByRole("button", { name: "Open B" }));
  await screen.findByText("B baseline");
  await act(async () => rejectCancel(new Error("old cancellation failed")));
  expect(screen.queryByText("old cancellation failed")).toBeNull();
  expect(screen.getByLabelText("Command state").textContent).toBe("idle");
  expect(screen.getByLabelText("Cancel state").textContent).toBe("idle");
});
