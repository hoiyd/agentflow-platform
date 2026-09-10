import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { AgentInfo, ChatMode } from "../../lib/api";
import { ChatComposer } from "./ChatComposer";

afterEach(cleanup);

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

function renderComposer(chatMode: ChatMode) {
  const noop = vi.fn();
  return render(
    <ChatComposer
      activeAgent={agent}
      activeAgentId={agent.id}
      agents={[agent]}
      agentsError=""
      chatMode={chatMode}
      completionVerificationEnabled={false}
      error=""
      input=""
      isAgentDescriptionExpanded={false}
      isAwaitingHumanInput={false}
      isAwaitingPlanApproval={false}
      isCreatingAgent={false}
      isNewAgentFormOpen={false}
      isStreaming={false}
      onAgentChange={noop}
      onConfigureAgent={noop}
      onDescriptionExpandedChange={noop}
      onInputChange={noop}
      onNewAgent={noop}
      onOpenVerification={noop}
      onSubmit={noop}
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
