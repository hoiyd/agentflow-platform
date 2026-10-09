import { useState } from "react";
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

// Failure inventory: descriptions must not duplicate the selected Agent name or
// expand the default toolbar; disclosure must remain accessible and reversible.
it("discloses the description separately without repeating the selected Agent name", () => {
  renderComposer("single");
  expect(screen.getAllByText("Test agent")).toHaveLength(1);
  expect(screen.queryByRole("region", {name: "Agent description"})).toBeNull();
  const details = screen.getByRole("button", {name: "Show agent description"});
  expect(details.getAttribute("aria-expanded")).toBe("false");
  expect(details.getAttribute("title")).toBe("Show agent description");
  fireEvent.click(details);
  const description = screen.getByRole("region", {name: "Agent description"});
  expect(description.textContent).toBe(agent.description);
  expect(details.getAttribute("aria-controls")).toBe(description.id);
  fireEvent.click(screen.getByRole("button", {name: "Hide agent description"}));
  expect(screen.queryByRole("region", {name: "Agent description"})).toBeNull();
});

it("keeps verification available without agent controls in collaborative modes", () => {
  const { container } = renderComposer("multi_agent");

  expect(screen.queryByRole("button", { name: "New agent" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Configure" })).toBeNull();
  expect(container.querySelector(".composer-run-options")?.contains(screen.getByRole("button", { name: /Verification/ }))).toBe(true);
});

it("leaves creation available but blocks sending and configuration in an empty Workspace", () => {
  const onSubmit = vi.fn();
  const view = renderComposer("single", [], "Do not use a template", vi.fn(), false, onSubmit, false);
  expect((screen.getByRole("button", { name: "New agent" }) as HTMLButtonElement).disabled).toBe(false);
  expect((screen.getByRole("button", { name: "Configure" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Send message" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Skill: No skills" }) as HTMLButtonElement).disabled).toBe(true);
  fireEvent.submit(view.container.querySelector("form")!);
  expect(onSubmit).not.toHaveBeenCalled();
});

it("invokes only bound skills and preserves the task text", () => {
  const onInputChange = vi.fn();
  const view = renderComposer("single", ["evidence-research", "knowledge-answer"], "/skill:evidence-research Find evidence", onInputChange);
  fireEvent.click(screen.getByRole("button", {name: "Skill: evidence-research"}));
  expect(screen.queryByRole("combobox", {name: "Search skills"})).toBeNull();
  expect(screen.getAllByRole("option").map(option => option.textContent)).toEqual(["Automatic", "evidence-research", "knowledge-answer"]);
  fireEvent.click(screen.getByRole("option", {name: "knowledge-answer", exact: true}));
  expect(onInputChange).toHaveBeenLastCalledWith("/skill:knowledge-answer Find evidence");
  fireEvent.click(screen.getByRole("button", {name: "Skill: evidence-research"}));
  fireEvent.click(screen.getByRole("option", {name: "Automatic", exact: true}));
  expect(onInputChange).toHaveBeenLastCalledWith("Find evidence");
  view.unmount();
  renderComposer("multi_agent", ["knowledge-answer"]);
  expect(screen.queryByRole("button", {name: /^Skill:/})).toBeNull();
});

it("keeps the Skill picker visible and disabled without bindings or while a Run is busy", () => {
  const view = renderComposer("single");
  const empty = screen.getByRole("button", {name: "Skill: No skills"});
  expect((empty as HTMLButtonElement).disabled).toBe(true);
  fireEvent.click(empty);
  expect(screen.queryByRole("listbox", {name: "Skill"})).toBeNull();
  view.unmount();
  renderComposer("single", ["writing"], "", vi.fn(), true);
  expect((screen.getByRole("button", {name: "Skill: Automatic"}) as HTMLButtonElement).disabled).toBe(true);
});

// A disabled control must explain its actual cause without a native title,
// and that explanation must also be available without a mouse.
it("explains No skills on hover and focus, distinguishing an absent Agent", () => {
  const view = renderComposer("single");
  const button = screen.getByRole("button", {name: "Skill: No skills"});
  expect(button.hasAttribute("title")).toBe(false);
  expect(screen.queryByRole("tooltip")).toBeNull();
  const noteTrigger = screen.getByRole("group", {name: "Skill: No skills"});
  fireEvent.pointerEnter(noteTrigger);
  expect(screen.getByRole("tooltip").textContent).toContain("No skills are assigned to this agent.");
  expect(noteTrigger.getAttribute("aria-describedby")).toBe(screen.getByRole("tooltip").id);
  fireEvent.pointerLeave(noteTrigger);
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.focus(noteTrigger);
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(noteTrigger, {key: "Escape"});
  expect(screen.queryByRole("tooltip")).toBeNull();
  fireEvent.focus(noteTrigger);
  fireEvent.blur(noteTrigger);
  expect(screen.queryByRole("tooltip")).toBeNull();
  view.unmount();
  renderComposer("single", [], "", vi.fn(), false, vi.fn(), false);
  fireEvent.pointerEnter(screen.getByRole("group", {name: "Skill: No skills"}));
  expect(screen.getByRole("tooltip").textContent).toContain("No agent is selected.");
});

function renderComposer(chatMode: ChatMode, skills: string[] = [], input = "", onInputChange = vi.fn(), isStreaming = false, onSubmit = vi.fn(), hasAgent = true) {
  return render(<ComposerFixture chatMode={chatMode} skills={skills} input={input} onInputChange={onInputChange}
    isStreaming={isStreaming} onSubmit={onSubmit} hasAgent={hasAgent} />);
}

function ComposerFixture({chatMode, skills, input, onInputChange, isStreaming, onSubmit, hasAgent}: {
  chatMode: ChatMode; skills: string[]; input: string; onInputChange: (value: string) => void;
  isStreaming: boolean; onSubmit: () => void; hasAgent: boolean;
}) {
  const noop = vi.fn();
  const [expanded, setExpanded] = useState(false);
  return (
    <ChatComposer
      activeAgent={hasAgent ? { ...agent, skills } : undefined}
      activeAgentId={hasAgent ? agent.id : ""}
      agents={hasAgent ? [agent] : []}
      agentsError=""
      chatMode={chatMode}
      completionVerificationEnabled={false}
      error=""
      input={input}
      isAgentDescriptionExpanded={expanded}
      isAwaitingHumanInput={false}
      isAwaitingPlanApproval={false}
      isCreatingAgent={false}
      isNewAgentFormOpen={false}
      isStreaming={isStreaming}
      onAgentChange={noop}
      onConfigureAgent={noop}
      onDescriptionExpandedChange={() => setExpanded(value => !value)}
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
