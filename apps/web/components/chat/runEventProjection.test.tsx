import { expect, it, vi } from "vitest";

import { createRunEventHandler, type RunState } from "./runEventProjection";

it("preserves the known agent when a durable run event omits agent_id", () => {
  let runState: RunState | null = {
    id: "run-1",
    agentId: "agent-worker",
    status: "running",
    verificationStatus: "not_required"
  };
  const handler = createRunEventHandler({
    assistantDraftId: "",
    defaultVerificationStatus: "not_required",
    fallbackAgentId: "agent-fallback",
    fallbackRunId: "run-1",
    setAutonomousProgress: vi.fn(),
    setCollaborationSteps: vi.fn(),
    setError: vi.fn(),
    setIsCancelingRun: vi.fn(),
    setMessages: vi.fn(),
    setPlanDraft: vi.fn(),
    setRunState: (update) => {
      runState = typeof update === "function" ? update(runState) : update;
    }
  });

  handler({
    type: "run_state",
    conversation_id: "conversation-1",
    run_id: "run-1",
    agent_id: "",
    status: "completed"
  });

  expect(runState?.agentId).toBe("agent-worker");
});
