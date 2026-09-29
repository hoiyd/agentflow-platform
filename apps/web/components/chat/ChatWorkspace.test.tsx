import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { Message } from "../../lib/api";
import { ChatWorkspace } from "./ChatWorkspace";

afterEach(cleanup);

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

function workspace(messages: Message[], isStreaming: boolean) {
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
  />;
}
