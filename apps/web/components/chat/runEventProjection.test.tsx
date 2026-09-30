import { expect, it, vi } from "vitest";

import { createRunEventHandler, type DraftMessage, type RunState } from "./runEventProjection";
import { readChatEventStream } from "../../lib/run-stream";

it("shows structured stream error identity without requiring raw server details", async () => {
  const setError = vi.fn();
  const handler = createRunEventHandler({
    assistantDraftId: "draft", defaultVerificationStatus: "not_required", fallbackAgentId: "", fallbackRunId: "run-1",
    setAutonomousProgress: vi.fn(), setCollaborationSteps: vi.fn(), setError, setIsCancelingRun: vi.fn(),
    setMessages: vi.fn(), setPlanDraft: vi.fn(), setRunState: vi.fn()
  });
  const event = { type: "error", error: "Internal Server Error", code: "invalid_request", source: "model_provider", request_id: "req-fixture" };
  await readChatEventStream(new Response(`data: ${JSON.stringify(event)}\n\n`), handler);
  expect(setError).toHaveBeenLastCalledWith("Internal Server Error [model_provider:invalid_request] (request req-fixture)");
  handler({ type: "error", error: "Connection unavailable" });
  expect(setError).toHaveBeenLastCalledWith("Connection unavailable");
});

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

it("projects streamed deltas and retracts only the current assistant draft", async () => {
  let messages: DraftMessage[] = [
    { id: "previous", role: "assistant", content: "Previous answer", conversation_id: "conv-1", created_at: "" },
    { id: "draft", role: "assistant", content: "", conversation_id: "conv-1", created_at: "" }
  ];
  const handler = createRunEventHandler({
    assistantDraftId: "draft", defaultVerificationStatus: "not_required", fallbackAgentId: "agent-1", fallbackRunId: "run-1",
    setAutonomousProgress: vi.fn(), setCollaborationSteps: vi.fn(), setError: vi.fn(), setIsCancelingRun: vi.fn(),
    setPlanDraft: vi.fn(), setRunState: vi.fn(),
    setMessages: (update) => { messages = typeof update === "function" ? update(messages) : update; }
  });
  async function frame(payload: Record<string, unknown>) {
    const data = { schema_version: 1, type: "model.delta", run_id: "run-1", payload };
    await readChatEventStream(new Response(`data: ${JSON.stringify(data)}\n\n`), handler);
  }
  await frame({ delta: "Checking source" });
  expect(messages[1].content).toBe("Checking source");
  await frame({ reset: true, delta: "" });
  expect(messages[1].content).toBe("");
  await frame({ delta: "Final " });
  expect(messages[1].content).toBe("Final ");
  await frame({ delta: "answer" });
  expect(messages.map((item) => item.content)).toEqual(["Previous answer", "Final answer"]);
});
