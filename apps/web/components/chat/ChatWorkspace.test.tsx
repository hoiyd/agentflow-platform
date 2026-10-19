import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

import type { Message } from "../../lib/api";
import { ChatWorkspace } from "./ChatWorkspace";
import type { ReasoningEntry } from "./ReasoningDisclosure";
import { ReasoningDisclosure } from "./ReasoningDisclosure";

afterEach(cleanup);

for (const mode of ["multi_agent", "autonomous"] as const) {
  it(`${mode} waits for command settlement before accepting human continuation`, () => {
    const onContinue = vi.fn();
    const onResume = vi.fn();
    const props: Partial<ComponentProps<typeof ChatWorkspace>> = {
      chatMode: mode, runStatus: "waiting_for_user", isCollaborationPanelOpen: true,
      showCollaborationPanel: true, showAutonomousTrace: mode === "autonomous",
      planDraft: "Approved plan", humanInputDraft: "Approved input", onContinue, onResume,
      collaborationSteps: mode === "multi_agent" ? [{ role: "planner", status: "completed" }]
        : [{ role: "human_input", status: "running", output: "Confirm" }]
    };
    const view = render(workspace([], true, [], props));
    const name = mode === "multi_agent" ? "Approve & Continue" : "Submit & Continue";
    expect((screen.getByRole("button", { name }) as HTMLButtonElement).disabled).toBe(true);
    view.rerender(workspace([], false, [], { ...props, isCanceling: true }));
    expect((screen.getByRole("button", { name }) as HTMLButtonElement).disabled).toBe(true);
    view.rerender(workspace([], false, [], props));
    expect((screen.getByRole("button", { name }) as HTMLButtonElement).disabled).toBe(false);
  });
}

it("shows live reasoning separately and drops unfinished text when the Run stops", () => {
  const entry: ReasoningEntry = { run_id: "run-1", turn_id: "turn-1", model_call_id: "call-1", format: "deepseek_reasoning_content", status: "receiving", text: "Safe live prefix" };
  const view = render(<ReasoningDisclosure entries={[entry]} runStatus="running" />);
  expect(screen.getByText("Safe live prefix")).toBeTruthy();
  for (const runStatus of ["canceled", "failed", "failed_recoverable", "completed", "waiting_for_user"]) {
    view.rerender(<ReasoningDisclosure entries={[entry]} runStatus={runStatus} />);
    expect(screen.queryByText("Safe live prefix")).toBeNull();
  }
  view.rerender(<ReasoningDisclosure entries={[{ ...entry, status: "complete" }]} runStatus="completed" />);
  expect(screen.getByText("Safe live prefix")).toBeTruthy();
});

it("keeps real provider reasoning collapsed and separate from the answer", () => {
  const reasoning: ReasoningEntry = { type: "model_reasoning", run_id: "run-1", turn_id: "turn-1", model_call_id: "call-1", format: "deepseek_reasoning_content", status: "complete", text: "Provider explanation", truncated: true };
  const view = render(workspace([message("answer", "assistant", "Final answer")], false, [reasoning]));
  const disclosure = screen.getByText(/^Provider reasoning/).closest("details");
  expect(disclosure?.hasAttribute("open")).toBe(false);
  expect(disclosure?.textContent).toContain("Display truncated");
  expect(screen.getByText("Final answer").closest(".bubble")?.textContent).not.toContain("Provider explanation");
  view.rerender(workspace([], false, []));
  expect(screen.queryByText(/^Provider reasoning/)).toBeNull();
});

it("shows a waiting status until text arrives, including Tool-round retraction", () => {
  const view = render(workspace([message("draft", "assistant", "")], true));
  expect(screen.getByRole("status").textContent).toBe("Working...");

  view.rerender(workspace([message("draft", "assistant", "First answer chunk")], true));
  expect(screen.queryByRole("status")).toBeNull();
  expect(screen.getByText("First answer chunk")).toBeTruthy();

  view.rerender(workspace([message("draft", "assistant", "")], true));
  expect(screen.getByRole("status").textContent).toBe("Working...");

  view.rerender(workspace([message("draft", "assistant", "")], false));
  expect(screen.queryByRole("status")).toBeNull();
});

it("does not show loading for historical empty messages or empty user messages", () => {
  const view = render(workspace([
    message("previous", "assistant", ""),
    message("draft", "assistant", "")
  ], true));
  expect(screen.getAllByRole("status")).toHaveLength(1);

  view.rerender(workspace([
    message("previous", "assistant", ""),
    message("user", "user", "")
  ], true));
  expect(screen.queryByRole("status")).toBeNull();
});

function message(id: string, role: Message["role"], content: string): Message {
  return { id, role, content, conversation_id: "conv-1", created_at: "2026-09-29T00:00:00Z" };
}

function workspace(messages: Message[], isStreaming: boolean, reasoning: ReasoningEntry[] = [], overrides: Partial<ComponentProps<typeof ChatWorkspace>> = {}) {
  const noop = vi.fn();
  return <ChatWorkspace
    agents={[]}
    autonomousProgress={null}
    chatMode="single"
    collaborationSteps={[]}
    humanInputDraft=""
    isCanceling={false}
    isCollaborationPanelOpen={false}
    isContinuing={false}
    isResuming={false}
    isStreaming={isStreaming}
    isTaskStatePanelOpen={false}
    messages={messages}
    reasoning={reasoning}
    messagesRef={{ current: null }}
    onCancel={noop}
    onContinue={noop}
    onHumanInputChange={noop}
    onModeChange={noop}
    onPanelOpenChange={noop}
    onPromptSelect={noop}
    onResume={noop}
    onRoleSelect={noop}
    onRoutingRequirementsChange={noop}
    onTaskStateClose={noop}
    onTaskStateRefresh={noop}
    onPlanDraftChange={noop}
    planDraft=""
    routingRequirements={{}}
    runStatus={isStreaming ? "running" : "failed"}
    selectedRole="planner"
    showAutonomousTrace={false}
    showCollaborationDag={false}
    showCollaborationPanel={false}
    taskState={null}
    taskStateError=""
    taskStateLoading={false}
    useExpandedConversationWidth={false}
    {...overrides}
  />;
}
