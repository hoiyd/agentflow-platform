import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useRunSession } from "./useRunSession";

const api = vi.hoisted(() => ({continueRun:vi.fn()}));
const questions = vi.hoisted(() => ({listClarifications:vi.fn(),answerClarification:vi.fn(),resumeClarification:vi.fn()}));
vi.mock("../../lib/api", async original => ({...await original<typeof import("../../lib/api")>(),...api}));
vi.mock("../../lib/clarification-api", () => questions);
const approvals = vi.hoisted(() => ({listToolApprovals:vi.fn(),decideToolApproval:vi.fn(),resumeToolApproval:vi.fn()}));
vi.mock("../../lib/tool-approval-api", () => approvals);
beforeEach(() => approvals.listToolApprovals.mockResolvedValue([]));
afterEach(() => { cleanup(); vi.resetAllMocks(); });

function mountSession() {
  const workspace = {activeId:"conversation-1",setActiveId:vi.fn(),setInput:vi.fn(),setMessages:vi.fn(),setError:vi.fn(),applyTitle:vi.fn()};
  const onReload = vi.fn(async () => {});
  const hook = renderHook(() => useRunSession({workspace,observing:false,onReload,
    onHistoryHandoff:vi.fn(),onSubmissionStart:() => () => {}}));
  act(() => hook.result.current.setRunState({id:"run-1",agentId:"agent-1",status:"failed_recoverable",verificationStatus:"not_required"}));
  return {...hook,workspace,onReload};
}

it("blocks both resume commands and plan continuation while hydration is pending or rejected", async () => {
  let reject!: (error: Error) => void;
  questions.listClarifications.mockImplementation(() => new Promise((_resolve, fail) => { reject = fail; }));
  const {result,workspace} = mountSession();
  await act(async () => {
    await result.current.resumeClarification("question-1");
    await result.current.continuePlan("plan");
  });
  expect(questions.resumeClarification).not.toHaveBeenCalled();
  expect(api.continueRun).not.toHaveBeenCalled();
  expect(workspace.setMessages).not.toHaveBeenCalled();
  await act(async () => reject(new Error("hydration failed")));
  await act(async () => { await result.current.resumeClarification("question-1"); });
  expect(questions.resumeClarification).not.toHaveBeenCalled();
  expect(workspace.setError).toHaveBeenLastCalledWith(expect.stringContaining("hydration failed"));
  expect(result.current.runState?.status).toBe("failed_recoverable");
});

it("blocks recovery until empty hydration commits, then reloads the authoritative status", async () => {
  let finishHydration!: (items: []) => void;
  questions.listClarifications.mockImplementation(() => new Promise(resolve => { finishHydration = resolve; }));
  const {result,onReload} = mountSession();
  await waitFor(() => expect(questions.listClarifications).toHaveBeenCalled());
  questions.resumeClarification.mockImplementation(async (_run, _question, emit) => emit({type:"done",run_id:"run-1",status:"completed"}));
  expect(result.current.clarification.ready).toBe(false);
  await act(async () => { await result.current.resumeClarification("question-1"); });
  expect(questions.resumeClarification).not.toHaveBeenCalled();
  expect(onReload).not.toHaveBeenCalled();
  expect(result.current.runState?.status).toBe("failed_recoverable");

  await act(async () => finishHydration([]));
  await waitFor(() => expect(result.current.clarification.isReady("run-1", "failed_recoverable")).toBe(true));
  await act(async () => { await result.current.resumeClarification("question-1"); });
  expect(questions.resumeClarification).toHaveBeenCalledTimes(1);
  expect(onReload).toHaveBeenCalledWith("conversation-1","resuming");
  expect(result.current.runState?.status).toBe("completed");
});

it("does not use a stale ready handler immediately after starting a same-status refresh", async () => {
  questions.listClarifications.mockResolvedValue([]);
  const {result} = mountSession();
  await waitFor(() => expect(result.current.clarification.ready).toBe(true));
  questions.listClarifications.mockImplementation(() => new Promise(() => {}));
  await act(async () => {
    result.current.clarification.refresh();
    await result.current.resumeClarification("question-1");
  });
  expect(questions.resumeClarification).not.toHaveBeenCalled();
});

it("checks the current Run ref before a retained ready handler can resume another Run", async () => {
  questions.listClarifications.mockResolvedValue([]);
  const {result} = mountSession();
  await waitFor(() => expect(result.current.clarification.ready).toBe(true));
  const resume = result.current.resumeClarification;
  questions.listClarifications.mockImplementation(() => new Promise(() => {}));
  await act(async () => {
    result.current.setRunState({id:"run-2",agentId:"agent-1",status:"failed_recoverable",verificationStatus:"not_required"});
    await resume("question-1");
  });
  expect(questions.resumeClarification).not.toHaveBeenCalled();
  expect(result.current.runState?.id).toBe("run-2");
});

it("does not let a historical consumed approval block a newer applied clarification", async () => {
  questions.listClarifications.mockResolvedValue([{ id: "question-new", status: "applied", created_at: "2026-11-17T01:00:00Z" }]);
  approvals.listToolApprovals.mockResolvedValue([{ id: "approval-old", status: "consumed", created_at: "2026-11-17T00:00:00Z" }]);
  questions.resumeClarification.mockResolvedValue(undefined);
  const { result } = mountSession();
  await waitFor(() => expect(result.current.clarification.ready && result.current.toolApprovals.ready).toBe(true));
  await act(async () => { await result.current.resumeClarification("question-new"); });
  expect(questions.resumeClarification).toHaveBeenCalledTimes(1);
});
