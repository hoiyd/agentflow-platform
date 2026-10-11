import { useEffect, useState } from "react";
import { getTaskState, getRunProjection, listMessages, listRuns, listConversations, listCollaborationSteps, type ChatMode, type TaskState } from "../../lib/api";
import { createLatestRequestController, type LatestRequestLease } from "../../lib/latest-request";
import { autonomousRoles } from "./AutonomousPanel";
import { toCollaborationStepView } from "./CollaborationPanels";
import type { useConversationWorkspace } from "./useConversationWorkspace";
import type { useRunSession } from "./useRunSession";

type Options = {
  workspace: Pick<ReturnType<typeof useConversationWorkspace>, "activeId" | "setActiveId" | "setConversations" | "setMessages" | "setError">;
  session: Pick<ReturnType<typeof useRunSession>, "detach" | "clearRun" | "setRunState" | "restoreOutputs" | "trace">;
  onModeRestored: (mode: ChatMode, preserveTaskState: boolean) => void;
};

// Navigation and Task State refresh have different invalidation boundaries.
// Run commands and SSE remain owned by useRunSession.
export function useConversationHistory({ workspace, session, onModeRestored: handleChatModeChange }: Options) {
  const { activeId, setActiveId, setConversations, setMessages, setError } = workspace;
  const { setRunState } = session;
  const { setCollaborationSteps, setPlanDraft, setHumanInputDraft } = session.trace;
  const [taskState, setTaskState] = useState<TaskState | null>(null);
  const [taskStateError, setTaskStateError] = useState("");
  const [isTaskStateLoading, setIsTaskStateLoading] = useState(false);
  const [historyRequests] = useState(createLatestRequestController);
  const [taskRequests] = useState(createLatestRequestController);
  const [conversationRequests] = useState(() => ({
    begin() {
      taskRequests.cancel();
      setIsTaskStateLoading(false);
      return historyRequests.begin();
    },
    cancel() {
      historyRequests.cancel();
      taskRequests.cancel();
      setIsTaskStateLoading(false);
    }
  }));
  useEffect(() => () => {
    historyRequests.cancel();
    taskRequests.cancel();
  }, [historyRequests, taskRequests]);

  function clearTaskState() {
    taskRequests.cancel();
    setTaskState(null);
    setTaskStateError("");
    setIsTaskStateLoading(false);
  }

  function resetConversationRuntimeState() {
    session.clearRun();
  }

  function activateConversation(id: string) {
    if (id !== activeId) {
      session.detach();
      resetConversationRuntimeState();
      setMessages([]);
      clearTaskState();
      setError("");
    }
    setActiveId(id);
  }

  async function refreshConversations(nextActiveId?: string) {
    const request = conversationRequests.begin();
    try {
      const items = await listConversations(request.signal);
      if (!request.isCurrent()) {
        return;
      }
      setConversations(items);
      const target = nextActiveId || (!activeId ? items[0]?.id : undefined);
      if (target) {
        activateConversation(target);
        await loadConversation(target, request);
      }
    } catch (err) {
      if (request.isCurrent()) {
        setError(err instanceof Error ? err.message : "Failed to load conversations");
      }
    }
  }

  async function loadConversation(
    conversationId: string,
    request: LatestRequestLease = conversationRequests.begin()
  ) {
    const taskRequest = taskRequests.begin();
    setIsTaskStateLoading(true);
    try {
      const [messagesResult, taskStateResult, traceResult] = await Promise.allSettled([
        listMessages(conversationId, request.signal),
        getTaskState(conversationId, taskRequest.signal).finally(() => {
          if (taskRequest.isCurrent()) setIsTaskStateLoading(false);
        }),
        readRunTrace(conversationId, request.signal)
      ]);
      if (!request.isCurrent()) {
        return;
      }
      if (messagesResult.status === "rejected") {
        throw messagesResult.reason;
      }
      setMessages(messagesResult.value);
      if (taskRequest.isCurrent()) {
        if (taskStateResult.status === "fulfilled") {
          setTaskState(taskStateResult.value);
          setTaskStateError("");
        } else {
          setTaskState(null);
          setTaskStateError(errorMessage(taskStateResult.reason, "Failed to load task state"));
        }
      }
      if (traceResult.status === "fulfilled") {
        restoreTrace(traceResult.value);
      } else {
        setError(errorMessage(traceResult.reason, "Failed to load run trace"));
      }
    } catch (err) {
      if (!request.isCurrent()) {
        return;
      }
      setMessages([]);
      resetConversationRuntimeState();
      setError(err instanceof Error ? err.message : "Failed to load conversation");
    } finally {
      if (taskRequest.isCurrent()) {
        setIsTaskStateLoading(false);
      }
    }
  }

  async function refreshTaskState(
    conversationId = activeId,
    navigation?: LatestRequestLease
  ) {
    const request = taskRequests.begin();
    if (!conversationId) {
      setTaskState(null);
      setTaskStateError("");
      setIsTaskStateLoading(false);
      return;
    }
    setIsTaskStateLoading(true);
    setTaskStateError("");
    try {
      const loaded = await getTaskState(conversationId, request.signal);
      if (request.isCurrent() && (!navigation || navigation.isCurrent())) {
        setTaskState(loaded);
      }
    } catch (err) {
      if (request.isCurrent() && (!navigation || navigation.isCurrent())) {
        setTaskStateError(err instanceof Error ? err.message : "Failed to load task state");
      }
    } finally {
      if (request.isCurrent() && (!navigation || navigation.isCurrent())) {
        setIsTaskStateLoading(false);
      }
    }
  }

  async function readRunTrace(conversationId: string, signal: AbortSignal) {
    const runs = await listRuns(signal);
    const run = runs.find((item) => item.conversation_id === conversationId);
    if (!run) return null;
    const [steps, projection] = await Promise.allSettled([
      listCollaborationSteps(run.id, signal), getRunProjection(run.id, signal)
    ]);
    return { run, steps, projection };
  }

  function restoreTrace(result: Awaited<ReturnType<typeof readRunTrace>>) {
    if (!result) {
      resetConversationRuntimeState();
      return;
    }
    const { run, steps: stepsResult, projection } = result;
    setRunState({ id: run.id, agentId: run.agent_id, status: run.status,
      verificationStatus: run.verification_status ?? "not_required" });
    const steps = stepsResult.status === "fulfilled" ? stepsResult.value : [];
    setCollaborationSteps(steps.map(toCollaborationStepView));
    if (projection.status === "fulfilled") session.restoreOutputs(projection.value);
    const errors = [
      stepsResult.status === "rejected" ? errorMessage(stepsResult.reason, "Failed to load run trace") : "",
      projection.status === "rejected" ? errorMessage(projection.reason, "Partial output recovery unavailable") : ""
    ].filter(Boolean);
    setError(errors.join("; "));
    if (steps.some((step) => autonomousRoles.some((role) => role.id === step.role))) {
      handleChatModeChange("autonomous", true);
    } else if (steps.length > 0) {
      handleChatModeChange("multi_agent", true);
    }
    setPlanDraft(steps.find((step) => step.role === "planner")?.output ?? "");
    const humanInput = steps.some((step) => step.role === "human_input" && step.status === "running");
    setHumanInputDraft((current) => humanInput ? current : "");
  }

  return { conversationRequests, activateConversation, refreshConversations, loadConversation, refreshTaskState,
    taskState, taskStateError, isTaskStateLoading, clearTaskState };
}


function errorMessage(error: unknown, label: string) {
  return error instanceof Error ? `${label}: ${error.message}` : label;
}

