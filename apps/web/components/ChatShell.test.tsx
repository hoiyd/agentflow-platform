import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ChatShell } from "./ChatShell";

const api = vi.hoisted(() => ({
  getAPIHealth: vi.fn(),
  listConversations: vi.fn(),
  listMessages: vi.fn(),
  getTaskState: vi.fn(),
  listRuns: vi.fn(),
  listCollaborationSteps: vi.fn(),
  listAgents: vi.fn(),
  listTools: vi.fn(),
  streamChat: vi.fn()
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
  ChatWorkspace: ({ messages, runStatus }: { messages: Array<{ content: string }>; runStatus: string }) => (
    <div>{messages.map((message, index) => <p key={index}>{message.content}</p>)}<output>{runStatus}</output></div>
  )
}));
vi.mock("./chat/ChatComposer", () => ({
  ChatComposer: ({ input, onInputChange, onSubmit }: {
    input: string; onInputChange: (value: string) => void; onSubmit: (event: React.FormEvent) => void;
  }) => <form onSubmit={onSubmit}><input aria-label="Prompt" onChange={(event) => onInputChange(event.target.value)} value={input} /><button>Send</button></form>
}));
vi.mock("./chat/ChatDialogs", () => ({ ChatDialogs: () => null }));

afterEach(cleanup);

it("opens Knowledge when returning from retrieval evaluation", async () => {
  setupAPI();
  render(<ChatShell initialView="knowledge" />);

  expect(await screen.findByRole("heading", { name: "Knowledge" })).toBeTruthy();
  expect(screen.getByRole("link", { name: "Evaluate index" })).toBeTruthy();
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
  api.listRuns.mockResolvedValue([]);
  api.listCollaborationSteps.mockResolvedValue([]);
  api.listAgents.mockResolvedValue([]);
  api.listTools.mockResolvedValue([]);
  knowledgeAPI.listDocuments.mockResolvedValue([]);
}
