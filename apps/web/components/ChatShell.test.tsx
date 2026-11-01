import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ChatShell } from "./ChatShell";

const api = vi.hoisted(() => ({
  getAPIHealth: vi.fn(),
  createConversation: vi.fn(),
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
const listClarifications = vi.hoisted(() => vi.fn());
vi.mock("../lib/clarification-api", () => ({listClarifications}));
const knowledgeAPI = vi.hoisted(() => ({ listDocuments: vi.fn() }));
vi.mock("../lib/knowledge-api", () => knowledgeAPI);
vi.mock("./chat/ChatChrome", () => ({
  Sidebar: ({ onOpenConversation, onNewConversation, isBusy }: {
    onOpenConversation: (id: string) => void; onNewConversation: () => void; isBusy: boolean;
  }) => {
    newConversationFromSidebar = onNewConversation;
    return <><button onClick={() => onOpenConversation("b")}>Open B</button>
      <button disabled={isBusy} onClick={onNewConversation}>New conversation</button></>;
  },
  Topbar: () => null,
  ToolsPanel: () => null
}));
vi.mock("./chat/ChatWorkspace", () => ({
  ChatWorkspace: ({ messages, planDraft, runStatus, isStreaming, isCanceling, clarificationLoading, clarificationError, onContinue, onResume, onCancel, onTaskStateRefresh, taskState, taskStateError, taskStateLoading }: {
    planDraft: string;
    messages: Array<{ content: string }>; runStatus: string; isStreaming: boolean; isCanceling: boolean;
    onContinue: () => void; onResume: (input?: string) => void; onCancel: () => void;
    onTaskStateRefresh: () => void; taskState: { version: number } | null; taskStateError: string;
    taskStateLoading: boolean;
    clarificationLoading?: boolean; clarificationError?: string;
  }) => (
    <div>{messages.map((message, index) => <p data-testid="message" key={index}>{message.content}</p>)}<output>{runStatus}</output><output>{planDraft}</output>
      <output aria-label="Command state">{isStreaming ? "busy" : "idle"}</output>
      <output aria-label="Cancel state">{isCanceling ? "canceling" : "idle"}</output>
      <button disabled={clarificationLoading || Boolean(clarificationError)} onClick={() => onContinue()}>Continue</button><button disabled={clarificationLoading || Boolean(clarificationError)} onClick={() => onResume()}>Resume</button><button onClick={onCancel}>Cancel run</button>
      <button disabled={clarificationLoading || Boolean(clarificationError)} onClick={() => onResume("answer")}>Resume with input</button>
      <button onClick={onTaskStateRefresh}>Refresh task state</button>
      <output aria-label="Task version">{taskState?.version}</output>
      <output aria-label="Task error">{taskStateError}</output>
      <output aria-label="Task loading">{taskStateLoading ? "loading" : "idle"}</output>
    </div>
  )
}));
let submitFromComposer: (event: React.FormEvent) => void;
let newConversationFromSidebar: () => void;
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

it("shares one clarification hydration authority between Workbench and session commands", async () => {
  setupAPI();
  api.listRuns.mockResolvedValue([{id:"waiting",conversation_id:"a",status:"waiting_for_user"}]);
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("waiting_for_user");
  await waitFor(() => expect(listClarifications).toHaveBeenCalledTimes(1));
});

// Creation may be slow, reject, or lose ownership to navigation. Never submit
// into the old Conversation, duplicate creation before render, or apply a late ID.
it("blocks stale submission and duplicate creation until the new conversation is ready", async () => {
  setupAPI();
  let finishCreation!: (conversation: unknown) => void;
  api.createConversation.mockImplementation(() => new Promise(resolve => { finishCreation = resolve; }));
  api.streamChat.mockImplementation(() => new Promise(() => {}));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "new task" } });
  const submit = submitFromComposer;
  const create = newConversationFromSidebar;
  act(() => {
    create();
    submit({ preventDefault() {} } as React.FormEvent);
    create();
  });
  expect(api.createConversation).toHaveBeenCalledTimes(1);
  expect(api.streamChat).not.toHaveBeenCalled();
  expect((screen.getByRole("button", { name: "New conversation" }) as HTMLButtonElement).disabled).toBe(true);
  await act(async () => finishCreation({ id: "c", title: "New conversation" }));
  await waitFor(() => expect((screen.getByRole("button", { name: "New conversation" }) as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByText("A baseline")).toBeNull();
  expect((screen.getByRole("textbox", { name: "Prompt" }) as HTMLInputElement).value).toBe("new task");
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(api.streamChat).toHaveBeenCalledTimes(1);
  expect(api.streamChat.mock.calls[0][0].conversation_id).toBe("c");
});

it("preserves the old conversation and draft after creation fails, and permits retry", async () => {
  setupAPI();
  let failCreation!: (error: Error) => void;
  api.createConversation.mockImplementationOnce(() => new Promise((_resolve, reject) => { failCreation = reject; }))
    .mockResolvedValueOnce({ id: "c", title: "New conversation" });
  api.streamChat.mockImplementation(() => new Promise(() => {}));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "retained draft" } });
  fireEvent.click(screen.getByRole("button", { name: "New conversation" }));
  await act(async () => failCreation(new Error("creation unavailable")));
  expect(screen.getByLabelText("Error").textContent).toBe("creation unavailable");
  expect(screen.getByText("A baseline")).toBeTruthy();
  expect((screen.getByRole("textbox", { name: "Prompt" }) as HTMLInputElement).value).toBe("retained draft");
  expect(api.streamChat).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "New conversation" }));
  await waitFor(() => expect(screen.queryByText("A baseline")).toBeNull());
  await waitFor(() => expect((screen.getByRole("button", { name: "New conversation" }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(api.streamChat.mock.calls[0][0].conversation_id).toBe("c");
});

it("does not apply a late created conversation after navigation", async () => {
  setupAPI();
  let finishCreation!: (conversation: unknown) => void;
  api.createConversation.mockImplementation(() => new Promise(resolve => { finishCreation = resolve; }));
  api.streamChat.mockImplementation(() => new Promise(() => {}));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  fireEvent.click(screen.getByRole("button", { name: "New conversation" }));
  fireEvent.click(screen.getByRole("button", { name: "Open B" }));
  await screen.findByText("B baseline");
  await act(async () => finishCreation({ id: "c", title: "New conversation" }));
  expect(screen.getByText("B baseline")).toBeTruthy();
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "B task" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  expect(api.streamChat.mock.calls[0][0].conversation_id).toBe("b");
});

it("refreshes task state without canceling pending conversation trace recovery", async () => {
  setupAPI();
  let finishSteps!: (steps: unknown[]) => void;
  api.listRuns.mockResolvedValue([{ id: "saved", conversation_id: "a", status: "completed" }]);
  api.listCollaborationSteps.mockImplementation(() => new Promise(resolve => { finishSteps = resolve; }));
  api.getTaskState.mockResolvedValueOnce({ version: 1 }).mockResolvedValueOnce({ version: 2 });
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.listCollaborationSteps).toHaveBeenCalledTimes(1));
  const traceSignal = api.listCollaborationSteps.mock.calls[0][1] as AbortSignal;
  await waitFor(() => expect(screen.getByLabelText("Task loading").textContent).toBe("idle"));
  fireEvent.click(screen.getByRole("button", { name: "Refresh task state" }));
  await waitFor(() => expect(screen.getByLabelText("Task version").textContent).toBe("2"));
  await act(async () => finishSteps([{ id: "planner", role: "planner", status: "completed", output: "recovered plan" }]));
  expect(traceSignal.aborted).toBe(false);
  expect(screen.getByText("recovered plan")).toBeTruthy();
  expect(screen.getByText("A baseline")).toBeTruthy();
});

it("does not let initial task hydration overwrite a newer explicit refresh", async () => {
  setupAPI();
  let finishInitial!: (state: unknown) => void;
  api.getTaskState.mockImplementationOnce(() => new Promise(resolve => { finishInitial = resolve; }))
    .mockResolvedValueOnce({ version: 2 });
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.getTaskState).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Refresh task state" }));
  await waitFor(() => expect(screen.getByLabelText("Task version").textContent).toBe("2"));
  await act(async () => finishInitial({ version: 1 }));
  expect(await screen.findByText("A baseline")).toBeTruthy();
  expect(screen.getByLabelText("Task version").textContent).toBe("2");
});

it("ignores task refresh results and errors from a previous conversation", async () => {
  setupAPI();
  let failTask!: (error: Error) => void;
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  api.getTaskState.mockImplementationOnce(() => new Promise((_resolve, reject) => { failTask = reject; }))
    .mockResolvedValueOnce({ version: 3 });
  fireEvent.click(screen.getByRole("button", { name: "Refresh task state" }));
  fireEvent.click(screen.getByRole("button", { name: "Open B" }));
  await screen.findByText("B baseline");
  await act(async () => failTask(new Error("late task failure")));
  expect(screen.getByLabelText("Task version").textContent).toBe("3");
  expect(screen.getByLabelText("Task error").textContent).toBe("");
});

it("accepts conversation messages and trace together after recovery reads finish", async () => {
  setupAPI();
  let finishSteps!: (steps: unknown[]) => void;
  api.listRuns.mockResolvedValue([{ id: "saved", conversation_id: "a", status: "completed" }]);
  api.listCollaborationSteps.mockImplementation(() => new Promise(resolve => { finishSteps = resolve; }));
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.listCollaborationSteps).toHaveBeenCalledTimes(1));
  expect(screen.queryByText("A baseline")).toBeNull();
  await act(async () => finishSteps([{ id: "planner", role: "planner", status: "completed", output: "complete history" }]));
  expect(screen.getByText("A baseline")).toBeTruthy();
  expect(screen.getByText("complete history")).toBeTruthy();
});

it("keeps authoritative run status and messages when stage recovery fails", async () => {
  setupAPI();
  api.listRuns.mockResolvedValue([{ id: "saved", conversation_id: "a", status: "failed_recoverable" }]);
  api.listCollaborationSteps.mockRejectedValue(new Error("stages unavailable"));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("Failed to load run trace: stages unavailable");
  expect(screen.getByText("failed_recoverable")).toBeTruthy();
  expect(screen.getByText("A baseline")).toBeTruthy();
});

it("discards late history reads after selecting another conversation", async () => {
  setupAPI();
  let finishMessages!: (messages: unknown[]) => void;
  api.listMessages.mockImplementationOnce(() => new Promise(resolve => { finishMessages = resolve; }))
    .mockResolvedValueOnce([{ content: "B baseline" }]);
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole("button", { name: "Open B" }));
  await screen.findByText("B baseline");
  await act(async () => finishMessages([{ content: "late A" }]));
  expect(screen.queryByText("late A")).toBeNull();
});

it("keeps messages when task state is unavailable and clears stale errors on navigation", async () => {
  setupAPI();
  api.getTaskState.mockRejectedValueOnce(new Error("task state unavailable"));
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  expect(screen.getByLabelText("Task error").textContent).toBe("Failed to load task state: task state unavailable");
  fireEvent.click(screen.getByRole("button", { name: "Open B" }));
  await screen.findByText("B baseline");
  expect(screen.getByLabelText("Task error").textContent).toBe("");
});

it("clears obsolete task loading when a new Run is accepted", async () => {
  setupAPI();
  api.getTaskState.mockImplementation(() => new Promise(() => {}));
  api.streamChat.mockImplementation((_input, emit) => {
    emit({ type: "conversation", conversation_id: "a" });
    emit({ type: "run_state", run_id: "accepted", conversation_id: "a", status: "running" });
    return new Promise(() => {});
  });
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.getTaskState).toHaveBeenCalledTimes(1));
  expect(screen.getByLabelText("Task loading").textContent).toBe("loading");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "new task" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await screen.findByText("running");
  expect(screen.getByLabelText("Task loading").textContent).toBe("idle");
});

it("clears old conversation data while URL navigation hydrates the next conversation", async () => {
  setupAPI();
  api.listRuns.mockResolvedValue([{ id: "saved", conversation_id: "a", status: "completed" }]);
  let failMessages!: (error: Error) => void;
  const view = render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  api.listMessages.mockImplementationOnce(() => new Promise((_resolve, reject) => { failMessages = reject; }));
  view.rerender(<ChatShell initialConversationId="b" />);
  await waitFor(() => expect(api.listMessages).toHaveBeenCalledWith("b", expect.anything()));
  expect(screen.queryByText("A baseline")).toBeNull();
  expect(screen.queryByText("completed")).toBeNull();
  await act(async () => failMessages(new Error("B history unavailable")));
  await screen.findByText("B history unavailable");
  expect(screen.queryByText("A baseline")).toBeNull();
});

it("preserves accepted Run status if refreshing the same conversation's Run list fails", async () => {
  setupAPI();
  api.streamChat.mockImplementation(async (_input, emit) => {
    emit({ type: "conversation", conversation_id: "a" });
    emit({ type: "run_state", run_id: "accepted", status: "completed" });
    emit({ type: "done", run_id: "accepted", conversation_id: "a", status: "completed" });
  });
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  api.listRuns.mockRejectedValue(new Error("run list unavailable"));
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "new task" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await screen.findByText("Failed to load run trace: run list unavailable");
  expect(screen.getByText("completed")).toBeTruthy();
  expect(screen.getByText("A baseline")).toBeTruthy();
});

it("keeps a provider failure after successful history recovery and clears it for the next command", async () => {
  setupAPI();
  api.listAgents.mockResolvedValue([{id:"agent-owned",name:"Owned agent",workspace_id:"default_workspace",system_prompt:"Help the user",tools:[],skills:[],is_template:false}]);
  api.streamChat.mockImplementationOnce(async (_input, emit) => {
    api.listRuns.mockResolvedValue([{ id: "rejected", conversation_id: "a", status: "failed" }]);
    emit({ type: "conversation", conversation_id: "a" });
    emit({ type: "run_state", run_id: "rejected", status: "failed" });
    emit({ type: "error", error: "Internal Server Error", code: "invalid_request", source: "model_provider", request_id: "req-fixture" });
    emit({ type: "done", run_id: "rejected", conversation_id: "a", status: "failed" });
  }).mockImplementationOnce(async (_input, emit) => {
    api.listRuns.mockResolvedValue([{ id: "accepted", conversation_id: "a", status: "completed" }]);
    emit({ type: "run_state", run_id: "accepted", status: "completed" });
    emit({ type: "done", run_id: "accepted", conversation_id: "a", status: "completed" });
  });
  render(<ChatShell initialConversationId="a" />);
  await screen.findByText("A baseline");
  const reads = api.listMessages.mock.calls.length;
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "rejected question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(reads + 1));
  await waitFor(() => expect(screen.getByLabelText("Command state").textContent).toBe("idle"));
  expect(screen.getByText("failed")).toBeTruthy();
  expect(screen.getByLabelText("Error").textContent).toBe("Internal Server Error [model_provider:invalid_request] (request req-fixture)");
  fireEvent.change(screen.getByRole("textbox", { name: "Prompt" }), { target: { value: "next question" } });
  fireEvent.click(screen.getByRole("button", { name: "Send" }));
  await screen.findByText("completed");
  await waitFor(() => expect(screen.getByLabelText("Command state").textContent).toBe("idle"));
  expect(screen.getByLabelText("Error").textContent).toBe("");
});

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
  listClarifications.mockResolvedValue([]);
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

// Failure inventory: old history may finish between draft insertion and the
// first SSE event; pre-acceptance rejection must reload history and retain input.
it("hands history ownership to Submit before conversation acceptance", async () => {
  setupAPI();
  let finishHistory!: (messages: unknown[]) => void;
  api.listMessages.mockImplementationOnce(() => new Promise(resolve => { finishHistory = resolve; }));
  api.streamChat.mockImplementation(() => new Promise(() => {}));
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(1));
  fireEvent.change(screen.getByRole("textbox", {name:"Prompt"}), {target:{value:"new pending question"}});
  fireEvent.click(screen.getByRole("button", {name:"Send"}));
  expect(api.streamChat).toHaveBeenCalledTimes(1);
  await act(async () => finishHistory([{id:"old",role:"assistant",content:"late old history"}]));
  expect(screen.getByText("new pending question")).toBeTruthy();
  expect(screen.queryByText("late old history")).toBeNull();
  await act(async () => {
    const emit=api.streamChat.mock.calls[0][1];
    emit({type:"conversation",conversation_id:"a"});
    emit({type:"run_state",run_id:"new",status:"running"});
    emit({type:"model_delta",delta:"visible streamed answer"});
  });
  expect(screen.getByText("visible streamed answer")).toBeTruthy();
});

it("restores valid history after a pre-acceptance rejection during initial load", async () => {
  setupAPI();
  let finishHistory!: (messages: unknown[]) => void;
  let rejectSubmit!: (error: Error) => void;
  api.listMessages.mockImplementationOnce(() => new Promise(resolve => { finishHistory=resolve; }));
  api.streamChat.mockImplementation(() => new Promise((_resolve,reject) => {rejectSubmit=reject;}));
  render(<ChatShell initialConversationId="a" />);
  await waitFor(() => expect(api.listMessages).toHaveBeenCalledTimes(1));
  fireEvent.change(screen.getByRole("textbox",{name:"Prompt"}),{target:{value:"retry this question"}});
  fireEvent.click(screen.getByRole("button",{name:"Send"}));
  await act(async()=>rejectSubmit(new Error("request not accepted")));
  expect(api.listMessages).toHaveBeenCalledTimes(2);
  await screen.findByText("A baseline");
  await act(async()=>finishHistory([{content:"obsolete initial read"}]));
  expect(screen.queryByText("obsolete initial read")).toBeNull();
  expect(screen.getByRole("textbox",{name:"Prompt"}).getAttribute("value")).toBe("retry this question");
  expect(screen.getByLabelText("Error").textContent).toBe("request not accepted");
});

it("recovers the initial conversation list when submission fails before any conversation is selected", async () => {
  setupAPI();
  let finishList!: (items: unknown[]) => void;
  api.listConversations.mockImplementationOnce(() => new Promise(resolve => {finishList=resolve;}));
  api.streamChat.mockRejectedValue(new Error("early rejection"));
  render(<ChatShell initialConversationId="" />);
  await waitFor(()=>expect(api.listConversations).toHaveBeenCalledTimes(1));
  fireEvent.change(screen.getByRole("textbox",{name:"Prompt"}),{target:{value:"early task"}});
  fireEvent.click(screen.getByRole("button",{name:"Send"}));
  await screen.findByText("A baseline");
  expect(api.listConversations).toHaveBeenCalledTimes(2);
  await act(async()=>finishList([{id:"obsolete",title:"obsolete list"}]));
  expect(screen.getByRole("textbox",{name:"Prompt"}).getAttribute("value")).toBe("early task");
  expect(screen.getByLabelText("Error").textContent).toBe("early rejection");
  expect(screen.queryByText("obsolete list")).toBeNull();
});

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
    let finishClarifications!: (items: []) => void;
    listClarifications.mockImplementationOnce(() => new Promise(resolve => { finishClarifications = resolve; }));
    api.listRuns.mockResolvedValue([{ id: "waiting", conversation_id: "a", status: "waiting_for_user", verification_status: "pending" }]);
    api.listCollaborationSteps.mockResolvedValue([{ id: "stage", role: command === "Continue" ? "planner" : "human_input", status: command === "Continue" ? "completed" : "running", output: "approved plan" }]);
    const request = command === "Continue" ? api.continueRun : api.resumeRun;
    request.mockRejectedValue(new Error("request rejected"));
    render(<ChatShell initialConversationId="a" />);
    await screen.findByText("waiting_for_user");
    if (command === "Continue") await screen.findByText("approved plan");
    const button = screen.getByRole("button", { name: command === "Resume" ? "Resume with input" : command });
    expect((button as HTMLButtonElement).disabled).toBe(true);
    expect(request).not.toHaveBeenCalled();
    await act(async () => finishClarifications([]));
    await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
    // Resume reads the supplied answer, not a fabricated Run/Stage identity.
    fireEvent.click(button);
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
