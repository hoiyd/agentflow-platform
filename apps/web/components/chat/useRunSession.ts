import { useCallback, useEffect, useLayoutEffect, useRef, useState, type SetStateAction } from "react";
import {
  cancelRun, continueRun, getRunProjection, observeRunEvents, resumeRun, streamChat,
  type AgentRoutingRequirements, type ChatMode
} from "../../lib/api";
import { createLatestRequestController } from "../../lib/latest-request";
import { removePendingMessages } from "../../lib/pending-messages";
import type { CompletionContractInput } from "../../lib/verification";
import { createRunEventHandler, isTerminalRunStatus, type DraftMessage, type RunState } from "./runEventProjection";
import type { useConversationWorkspace } from "./useConversationWorkspace";
import { useRunTrace } from "./useRunTrace";
import type { ReasoningEntry } from "./ReasoningDisclosure";
import type { PartialOutput, RunProjectionSnapshot } from "../../lib/api-types";

type Command =
  | { kind: "submitting"; content: string; mode: ChatMode; agentId: string; completionContract?: CompletionContractInput }
  | { kind: "continuing"; plan: string; requirements: AgentRoutingRequirements }
  | { kind: "resuming"; input: string };
type Activity = "idle" | Command["kind"];
type Workspace = Pick<ReturnType<typeof useConversationWorkspace>,
  "activeId" | "setActiveId" | "setInput" | "setMessages" | "setError" | "applyTitle">;
type SessionOptions = {
  workspace: Workspace;
  observing: boolean;
  onReload: (conversationId: string, command: Command["kind"] | "observed") => Promise<void>;
  onConversationAccepted: () => void;
  // View layout remains owned by ChatShell; return its pre-acceptance rollback.
  onSubmissionStart: (mode: ChatMode) => () => void;
};
type EventOptions = Omit<Parameters<typeof createRunEventHandler>[0],
  "setAutonomousProgress" | "setCollaborationSteps" | "setError" | "setMessages" | "setPlanDraft" | "setRunState" | "onReasoning">;

// One owner for command admission, optimistic drafts and connection leases.
// Activity is not the durable Run status; cancellation may overlap a command.
export function useRunSession(options: SessionOptions) {
  const trace = useRunTrace();
  const latest = useRef({ options, trace });
  useLayoutEffect(() => { latest.current = { options, trace }; });
  const [activity, setActivity] = useState<Activity>("idle");
  const activityRef = useRef<Activity>("idle");
  const [isCancelingRun, setCanceling] = useState(false);
  const cancelInFlight = useRef(false);
  const [runState, setStoredRunState] = useState<RunState | null>(null);
  // Pending display updates; completed history is loaded through message.reasoning.
  const [reasoning, setReasoning] = useState<ReasoningEntry[]>([]);
  const [partialOutputs, setPartialOutputs] = useState<PartialOutput[]>([]);
  const outputCursor = useRef({ runId: "", sequence: -1 });
  const runRef = useRef<RunState | null>(null);
  const [streams] = useState(createLatestRequestController);
  const [cancellations] = useState(createLatestRequestController);
  const [observations] = useState(createLatestRequestController);

  // Keep async ownership checks current even before React flushes a render.
  const setRunState = useCallback((update: SetStateAction<RunState | null>) => {
    const next = typeof update === "function" ? update(runRef.current) : update;
    runRef.current = next;
    setStoredRunState(next);
    if (!next || isTerminalRunStatus(next.status)) {
      cancelInFlight.current = false;
      setCanceling(false);
    }
  }, []);

  const restoreOutputs = useCallback((snapshot: RunProjectionSnapshot) => {
    if (snapshot.run.run_id !== runRef.current?.id) return;
    const previous = outputCursor.current;
    if (previous.runId === snapshot.run.run_id && previous.sequence > snapshot.as_of_sequence) return;
    outputCursor.current = { runId: snapshot.run.run_id, sequence: snapshot.as_of_sequence };
    setPartialOutputs(snapshot.run.status === "completed" ? [] : snapshot.partial_outputs ?? []);
  }, []);

  const { setAutonomousProgress, setCollaborationSteps, setPlanDraft } = trace;
  const { setMessages, setError } = options.workspace;
  const createHandler = useCallback((context: EventOptions) => createRunEventHandler({
    ...context, setAutonomousProgress, setCollaborationSteps, setPlanDraft,
    setMessages, setError, setRunState,
    onReasoning: (entry) => setReasoning((items) => {
      if (entry.run_id !== runRef.current?.id) return items;
      const index = items.findIndex((item) => item.run_id === entry.run_id && item.turn_id === entry.turn_id && item.model_call_id === entry.model_call_id);
      // Replay's text-free receiving fact must not erase a newer live prefix;
      // late live batches must not overwrite a terminal display.
      if (index >= 0) {
        if (items[index].status !== "receiving" || (entry.status === "receiving" && !entry.text && items[index].text)) return items;
        return items.map((item, i) => i === index ? entry : item);
      }
      return [...items, entry].slice(-32);
    })
  }), [setAutonomousProgress, setCollaborationSteps, setPlanDraft, setMessages, setError, setRunState]);

  useEffect(() => () => {
    streams.cancel();
    cancellations.cancel();
    observations.cancel();
  }, [streams, cancellations, observations]);

  const conversationId = options.workspace.activeId;
  const observing = options.observing;
  useEffect(() => {
    const run = runRef.current;
    if (!conversationId || !observing || activity !== "idle" || !run ||
      !["queued", "running", "canceling"].includes(run.status)) return;

    const request = observations.begin();
    let refreshed = false;
    const reloadStoppedRun = (status: string) => {
      if (!request.isCurrent() || !isStoppedRunStatus(status) || refreshed) return;
      refreshed = true;
      void latest.current.options.onReload(conversationId, "observed");
    };
    const handle = createHandler({
      assistantDraftId: "", defaultVerificationStatus: run.verificationStatus,
      fallbackAgentId: run.agentId, fallbackRunId: run.id,
      onRunState: (event) => reloadStoppedRun(event.status)
    });
    void observeRunEvents(run.id, {
      signal: request.signal,
      onEvent: (event, _sequence, replayed) => {
        if (!request.isCurrent()) return;
        // Historical lifecycle events rebuild details, not current Run status.
        if (!replayed || event.type !== "run_state") handle(event);
      },
      onSnapshot: (snapshot) => {
        if (!request.isCurrent() || snapshot.run.conversation_id !== conversationId || snapshot.run.run_id !== run.id) return;
        setRunState({ id: run.id, agentId: run.agentId, status: snapshot.run.status,
          verificationStatus: snapshot.run.verification_status });
        restoreOutputs(snapshot);
        reloadStoppedRun(snapshot.run.status);
      },
      onOutput: (output) => {
        if (!request.isCurrent() || output.run_id !== runRef.current?.id) return;
        const previous = outputCursor.current;
        if (previous.runId === output.run_id && previous.sequence >= output.sequence) return;
        outputCursor.current = { runId: output.run_id, sequence: output.sequence };
        setPartialOutputs(items => [...items.filter(item => !(item.turn_id === output.turn_id && item.channel === output.channel &&
          (output.channel === "answer" || item.model_call_id === output.model_call_id))), output].slice(-32));
      }
    }).catch((error) => {
      if (request.isCurrent()) setError(error instanceof Error ? `Live run updates unavailable: ${error.message}` : "Live run updates unavailable");
    });
    return () => observations.cancel();
  }, [conversationId, observing, activity, runState?.id, runState?.status, observations, createHandler, setRunState, setError, restoreOutputs]);

  function clearRun() {
    cancellations.cancel();
    setRunState(null);
    trace.reset();
    setReasoning([]);
    setPartialOutputs([]);
    outputCursor.current = { runId: "", sequence: -1 };
  }

  // Detaching closes browser-owned requests, never cancels the backend Run.
  function detach() {
    streams.cancel();
    cancellations.cancel();
    observations.cancel();
    activityRef.current = "idle";
    setActivity("idle");
    cancelInFlight.current = false;
    setCanceling(false);
    setReasoning([]);
    setPartialOutputs([]);
    outputCursor.current = { runId: "", sequence: -1 };
  }

  async function execute(command: Command) {
    if (activityRef.current !== "idle") return;
    const previousRun = runRef.current;
    const previousReasoning = reasoning;
    const previousPartialOutputs = partialOutputs;
    const previousOutputCursor = outputCursor.current;
    if (command.kind !== "submitting" && !previousRun?.id) return;
    activityRef.current = command.kind;
    setActivity(command.kind);
    observations.cancel();
    const request = streams.begin();
    const { workspace, onSubmissionStart } = latest.current.options;
    const previousTrace = latest.current.trace;
    const runId = previousRun?.id ?? "";
    let conversationId = workspace.activeId;
    let rollbackLayout: (() => void) | undefined;
    workspace.setError("");
    if (command.kind === "submitting") {
      workspace.setInput("");
      clearRun();
      rollbackLayout = onSubmissionStart(command.mode);
    } else if (command.kind === "continuing") {
      trace.setPlanDraft(command.plan);
    } else {
      trace.setHumanInputDraft(command.input);
      setRunState(previousRun ? { ...previousRun, status: "running" } : null);
    }

    const assistant: DraftMessage = { id: `local-assistant-${crypto.randomUUID()}`, conversation_id: conversationId,
      role: "assistant", content: "", created_at: new Date().toISOString() };
    const drafts: DraftMessage[] = command.kind === "submitting"
      ? [{ ...assistant, id: `local-user-${crypto.randomUUID()}`, role: "user", content: command.content }, assistant]
      : [assistant];
    workspace.setMessages((items) => [...items, ...drafts]);
    let accepted = false;
    const handle = createHandler({
      assistantDraftId: assistant.id,
      defaultVerificationStatus: command.kind === "submitting"
        ? command.completionContract ? "pending" : "not_required"
        : previousRun?.verificationStatus ?? "not_required",
      fallbackAgentId: command.kind === "submitting" ? command.agentId : previousRun?.agentId ?? "",
      fallbackRunId: command.kind === "submitting" ? "" : runId,
      onEvent: () => { accepted = true; },
      onConversation: (event) => {
        conversationId = event.conversation_id;
        latest.current.options.onConversationAccepted();
        workspace.setActiveId(conversationId);
      },
      onDone: (event) => workspace.applyTitle(event.conversation_id, event.title),
      onRunState: (event) => {
        if (command.kind === "resuming" && event.status !== "waiting_for_user") trace.setHumanInputDraft("");
      }
    });
    const onEvent: Parameters<typeof streamChat>[1] = (event) => { if (request.isCurrent()) handle(event); };
    try {
      if (command.kind === "submitting") {
        await streamChat({ conversation_id: conversationId || undefined, agent_id: command.agentId || undefined,
          mode: command.mode, message: command.content, completion_contract: command.completionContract }, onEvent, request.signal);
      } else if (command.kind === "continuing") {
        await continueRun({ run_id: runId, plan: command.plan, routing_requirements: command.requirements }, onEvent, request.signal);
      } else {
        await resumeRun({ run_id: runId, user_input: command.input }, onEvent, request.signal);
      }
      if (request.isCurrent()) await latest.current.options.onReload(conversationId, command.kind);
    } catch (error) {
      if (!request.isCurrent()) return;
      if (!accepted) {
        workspace.setMessages((items) => removePendingMessages(items, drafts.map((draft) => draft.id)));
        if (command.kind === "submitting") {
          workspace.setInput(command.content);
          setRunState(previousRun);
          setReasoning(previousReasoning);
          setPartialOutputs(previousPartialOutputs);
          outputCursor.current = previousOutputCursor;
          trace.restore(previousTrace);
          rollbackLayout?.();
        } else if (command.kind === "resuming") {
          setRunState(previousRun);
        }
      }
      workspace.setError(error instanceof Error ? error.message : `Unexpected ${command.kind === "submitting" ? "chat" : command.kind === "continuing" ? "continue" : "resume"} error`);
      if (accepted && runRef.current?.id) {
        try {
          const snapshot = await getRunProjection(runRef.current.id, request.signal);
          if (request.isCurrent()) {
            restoreOutputs(snapshot);
            if (snapshot.partial_outputs?.some(item => item.channel === "answer" && item.text)) {
              workspace.setMessages(items => items.filter(item => item.id !== assistant.id));
            }
          }
        } catch { /* Keep the visible draft and original error if recovery is unavailable. */ }
      }
    } finally {
      if (request.isCurrent()) {
        activityRef.current = "idle";
        setActivity("idle");
      }
    }
  }

  async function cancel() {
    const run = runRef.current;
    if (!run || cancelInFlight.current || isTerminalRunStatus(run.status)) return;
    const request = cancellations.begin();
    cancelInFlight.current = true;
    setCanceling(true);
    setError("");
    const isCurrent = () => request.isCurrent() && runRef.current?.id === run.id && !isTerminalRunStatus(runRef.current.status);
    try {
      const canceled = await cancelRun(run.id);
      if (!isCurrent()) return;
      setRunState({ id: canceled.id, agentId: canceled.agent_id, status: canceled.status,
        verificationStatus: canceled.verification_status ?? "not_required" });
    } catch (error) {
      if (!isCurrent()) return;
      setError(error instanceof Error ? error.message : "Failed to cancel run");
      cancelInFlight.current = false;
      setCanceling(false);
    }
  }

  return {
    runState, setRunState, trace, activity, reasoning, partialOutputs, restoreOutputs, isCancelingRun, clearRun, detach, cancel,
    isStreaming: activity !== "idle", isContinuingRun: activity === "continuing", isResumingRun: activity === "resuming",
    submit: (content: string, settings: { mode: ChatMode; agentId: string; completionContract?: CompletionContractInput }) =>
      content.trim() ? execute({ kind: "submitting", content: content.trim(), ...settings }) : Promise.resolve(),
    continuePlan: (plan?: string, requirements?: AgentRoutingRequirements) => {
      const current = latest.current.trace;
      const approved = (plan ?? current.planDraft).trim();
      return approved ? execute({ kind: "continuing", plan: approved, requirements: requirements ?? current.routingRequirements }) : Promise.resolve();
    },
    resume: (input?: string) => {
      const answer = (input ?? latest.current.trace.humanInputDraft).trim();
      return answer ? execute({ kind: "resuming", input: answer }) : Promise.resolve();
    }
  };
}

function isStoppedRunStatus(status: string) {
  return status === "waiting_for_user" || isTerminalRunStatus(status);
}
