import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { useRunSession } from "./useRunSession";

const api = vi.hoisted(() => ({resumeRun:vi.fn(),continueRun:vi.fn()}));
const questions = vi.hoisted(() => ({listClarifications:vi.fn(),answerClarification:vi.fn(),resumeClarification:vi.fn()}));
vi.mock("../../lib/api", async original => ({...await original<typeof import("../../lib/api")>(),...api}));
vi.mock("../../lib/clarification-api", () => questions);
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
    await result.current.resume("resume");
    await result.current.resumeClarification("question-1");
    await result.current.continuePlan("plan");
  });
  expect(api.resumeRun).not.toHaveBeenCalled();
  expect(questions.resumeClarification).not.toHaveBeenCalled();
  expect(api.continueRun).not.toHaveBeenCalled();
  expect(workspace.setMessages).not.toHaveBeenCalled();
  await act(async () => reject(new Error("hydration failed")));
  await act(async () => { await result.current.resume("resume"); await result.current.resumeClarification("question-1"); });
  expect(api.resumeRun).not.toHaveBeenCalled();
  expect(questions.resumeClarification).not.toHaveBeenCalled();
  expect(workspace.setError).toHaveBeenLastCalledWith(expect.stringContaining("hydration failed"));
  expect(result.current.runState?.status).toBe("failed_recoverable");
});

it("allows loaded-empty generic recovery and reloads the authoritative status", async () => {
  questions.listClarifications.mockResolvedValue([]);
  const {result,onReload} = mountSession();
  await waitFor(() => expect(questions.listClarifications).toHaveBeenCalled());
  api.resumeRun.mockImplementation(async (_input, emit) => emit({type:"done",run_id:"run-1",status:"completed"}));
  await act(async () => { await result.current.resume("resume"); });
  expect(api.resumeRun).toHaveBeenCalledTimes(1);
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
    await result.current.resume("resume");
  });
  expect(api.resumeRun).not.toHaveBeenCalled();
});

it("checks the current Run ref before a retained ready handler can resume another Run", async () => {
  questions.listClarifications.mockResolvedValue([]);
  const {result} = mountSession();
  await waitFor(() => expect(result.current.clarification.ready).toBe(true));
  const resume = result.current.resume;
  questions.listClarifications.mockImplementation(() => new Promise(() => {}));
  await act(async () => {
    result.current.setRunState({id:"run-2",agentId:"agent-1",status:"failed_recoverable",verificationStatus:"not_required"});
    await resume("resume");
  });
  expect(api.resumeRun).not.toHaveBeenCalled();
  expect(result.current.runState?.id).toBe("run-2");
});
