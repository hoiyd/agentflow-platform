import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { AgentInfo, ChatMode } from "../../lib/api";
import { ChatComposer } from "./ChatComposer";

afterEach(cleanup);

it("does not submit ordinary Chat while a durable Run is busy, including keyboard submission", () => {
  const onSubmit = vi.fn();
  const view = renderComposer("single", [], "Next instruction", vi.fn(), true, onSubmit);
  fireEvent.submit(view.container.querySelector("form")!);
  expect(onSubmit).not.toHaveBeenCalled();
});

it("keeps single-agent controls in one compact action group", () => {
  const { container } = renderComposer("single");
  const actions = container.querySelector(".agent-actions");

  expect(actions?.contains(screen.getByRole("button", { name: "New agent" }))).toBe(true);
  expect(actions?.contains(screen.getByRole("button", { name: "Configure" }))).toBe(true);
  expect(actions?.contains(screen.getByRole("button", { name: /Verification/ }))).toBe(true);
  expect(container.querySelector(".composer-run-options")).toBeNull();
});

it("keeps verification available without agent controls in collaborative modes", () => {
  const { container } = renderComposer("multi_agent");

  expect(screen.queryByRole("button", { name: "New agent" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Configure" })).toBeNull();
  expect(container.querySelector(".composer-run-options")?.contains(screen.getByRole("button", { name: /Verification/ }))).toBe(true);
});

it("invokes only bound skills and preserves the task text", () => {
  const onInputChange = vi.fn();
  const view = renderComposer("single", ["evidence-research", "knowledge-answer"], "/skill:evidence-research Find evidence", onInputChange);
  fireEvent.change(screen.getByRole("combobox", { name: "Invoke skill" }), { target: { value: "knowledge-answer" } });
  expect(onInputChange).toHaveBeenLastCalledWith("/skill:knowledge-answer Find evidence");
  fireEvent.change(screen.getByRole("combobox", { name: "Invoke skill" }), { target: { value: "" } });
  expect(onInputChange).toHaveBeenLastCalledWith("Find evidence");
  view.unmount();
  renderComposer("multi_agent", ["knowledge-answer"]);
  expect(screen.queryByRole("combobox", { name: "Invoke skill" })).toBeNull();
});

function renderComposer(chatMode: ChatMode, skills: string[] = [], input = "", onInputChange = vi.fn(), isStreaming = false, onSubmit = vi.fn()) {
  const noop = vi.fn();
  return render(
    <ChatComposer
      activeAgent={{ ...agent, skills }}
      activeAgentId={agent.id}
      agents={[agent]}
      agentsError=""
      chatMode={chatMode}
      completionVerificationEnabled={false}
      error=""
      input={input}
      isAgentDescriptionExpanded={false}
      isAwaitingHumanInput={false}
      isAwaitingPlanApproval={false}
      isCreatingAgent={false}
      isNewAgentFormOpen={false}
      isStreaming={isStreaming}
      onAgentChange={noop}
      onConfigureAgent={noop}
      onDescriptionExpandedChange={noop}
      onInputChange={onInputChange}
      onNewAgent={noop}
      onOpenVerification={noop}
      onSubmit={onSubmit}
      showAgentActions
    />
  );
}

const agent: AgentInfo = {
  id: "agent-1",
  name: "Test agent",
  description: "Test description",
  system_prompt: "You are a test agent.",
  tools: [],
  memory_enabled: false,
  retrieval_enabled: false,
  created_at: "2026-09-10T00:00:00Z",
  updated_at: "2026-09-10T00:00:00Z"
};
