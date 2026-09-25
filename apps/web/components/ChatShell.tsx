"use client";

import { FormEvent, useEffect, useRef, useState } from "react";
import {
  AgentRoutingRequirements,
  ChatMode,
  TaskState,
  cancelRun,
  continueRun,
  createConversation,
  deleteConversation as deleteConversationApi,
  getAPIHealth,
  listCollaborationSteps,
  listConversations,
  listRuns,
  listMessages,
  getTaskState,
  observeRunEvents,
  resumeRun,
  streamChat
} from "../lib/api";
import { buildCompletionContract } from "../lib/verification";
import { createLatestRequestController, type LatestRequestLease } from "../lib/latest-request";
import { removePendingMessages } from "../lib/pending-messages";
import { Sidebar, ToolsPanel, Topbar, type APIConnectionStatus, type ChatView } from "./chat/ChatChrome";
import { ChatComposer } from "./chat/ChatComposer";
import { ChatDialogs } from "./chat/ChatDialogs";
import { ChatWorkspace } from "./chat/ChatWorkspace";
import { createRunEventHandler, type DraftMessage, type RunState } from "./chat/runEventProjection";
import { useAgentManagement } from "./chat/useAgentManagement";
import { useCompletionVerification } from "./chat/useCompletionVerification";
import { useConversationWorkspace } from "./chat/useConversationWorkspace";
import { useToolCatalog } from "./chat/useToolCatalog";
import { autonomousRoles } from "./chat/AutonomousPanel";
import { toCollaborationStepView } from "./chat/CollaborationPanels";
import { useRunTrace } from "./chat/useRunTrace";
import { KnowledgePanel } from "./knowledge/KnowledgePanel";
import { useKnowledgeWorkbench } from "./knowledge/useKnowledgeWorkbench";
import { MemoryPanel } from "./memory/MemoryPanel";
import { useMemoryWorkbench } from "./memory/useMemoryWorkbench";
import { OperatorAttentionPanel } from "./attention/OperatorAttentionPanel";

type ChatShellProps = {
  initialConversationId?: string;
  initialView?: ChatView;
};

type ChatSidePanel = "trace" | "task_state" | "closed";

export function ChatShell({ initialConversationId = "", initialView = "chat" }: ChatShellProps) {
  const {
    conversations, setConversations, activeId, setActiveId, activeConversation,
    messages, setMessages, input, setInput, error, setError,
    editingConversationId, conversationTitleDraft, isSavingConversationTitle,
    startEditingTitle, cancelEditingTitle, setConversationTitleDraft, saveTitle, applyTitle
  } = useConversationWorkspace();
  const [isStreaming, setIsStreaming] = useState(false);
  const [chatMode, setChatMode] = useState<ChatMode>("multi_agent");
  const [sidePanel, setSidePanel] = useState<ChatSidePanel>("trace");
  const [taskState, setTaskState] = useState<TaskState | null>(null);
  const [taskStateError, setTaskStateError] = useState("");
  const [isTaskStateLoading, setIsTaskStateLoading] = useState(false);
  const [isContinuingRun, setIsContinuingRun] = useState(false);
  const [isResumingRun, setIsResumingRun] = useState(false);
  const [isCancelingRun, setIsCancelingRun] = useState(false);
  const [runState, setRunState] = useState<RunState | null>(null);
  const currentRunId = useRef(runState?.id);
  currentRunId.current = runState?.id;
  const [view, setView] = useState<ChatView>(initialView);
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false);
  const [apiConnectionStatus, setAPIConnectionStatus] = useState<APIConnectionStatus>("checking");
  const messagesRef = useRef<HTMLElement | null>(null);
  const conversationRequestsRef = useRef<ReturnType<typeof createLatestRequestController> | null>(null);
  if (!conversationRequestsRef.current) {
    conversationRequestsRef.current = createLatestRequestController();
  }
  const conversationRequests = conversationRequestsRef.current;
  const streamRequestsRef = useRef<ReturnType<typeof createLatestRequestController> | null>(null);
  if (!streamRequestsRef.current) streamRequestsRef.current = createLatestRequestController();
  const streamRequests = streamRequestsRef.current;
  const navigationRevision = useRef(0);
  const knowledge = useKnowledgeWorkbench();
  const memory = useMemoryWorkbench();
  const {
    collaborationSteps, setCollaborationSteps, autonomousProgress, setAutonomousProgress,
    humanInputDraft, setHumanInputDraft, selectedCollaborationRole, setSelectedCollaborationRole,
    planDraft, setPlanDraft, routingRequirements, setRoutingRequirements, reset: resetRunTrace
  } = useRunTrace();
  const verification = useCompletionVerification();
  const toolCatalog = useToolCatalog();
  const {
    agents, activeAgent, activeAgentId, isAgentDescriptionExpanded, agentsError,
    isAgentConfigOpen, agentConfigDraft, newAgentDraft, isNewAgentFormOpen,
    isSavingAgentConfig, isCreatingAgent, archivingAgentId, agentArchiveCandidate,
    agentOperationNotice, agentConfigStatus, refreshAgents, closeAgentForms,
    selectAgent, openAgentConfig, toggleDescription, dismissNotice,
    updateAgentConfigDraft, updateNewAgentDraft, toggleAgentConfigTool, toggleNewAgentTool,
    handleSaveAgentConfig, handleOpenNewAgentForm, handleCancelNewAgent, handleCancelAgentConfig,
    handleCreateAgent, handleArchiveAgent, confirmArchiveAgent, cancelArchiveAgent
  } = useAgentManagement(isStreaming, () => setRunState(null));

  const showCollaborationPanel = chatMode === "multi_agent" || chatMode === "autonomous";
  const showCollaborationDag = chatMode === "multi_agent";
  const showAutonomousTrace = chatMode === "autonomous";
  const isCollaborationPanelOpen = showCollaborationPanel && sidePanel === "trace";
  const isTaskStatePanelOpen = sidePanel === "task_state";
  const useExpandedConversationWidth =
    !isTaskStatePanelOpen && (chatMode === "single" || (showCollaborationPanel && !isCollaborationPanelOpen));
  const isAwaitingPlanApproval = chatMode === "multi_agent" && runState?.status === "waiting_for_user";
  const isAwaitingHumanInput =
    chatMode === "autonomous" &&
    runState?.status === "waiting_for_user" &&
    collaborationSteps.some((step) => step.role === "human_input" && step.status === "running");
  const isTerminalRun =
    runState?.status === "completed" ||
    runState?.status === "failed" ||
    runState?.status === "failed_recoverable" ||
    runState?.status === "canceled";
  const canCancelRun =
    chatMode === "autonomous" &&
    !!runState?.id &&
    !isTerminalRun &&
    (isStreaming ||
      runState.status === "running" ||
      runState.status === "canceling" ||
      runState.status === "waiting_for_user");
  const isRunStreaming =
    isStreaming ||
    isContinuingRun ||
    isResumingRun ||
    runState?.status === "running" ||
    runState?.status === "canceling";
  const visibleCollaborationRole =
    selectedCollaborationRole === "planner" ||
    collaborationSteps.some((step) => step.role === selectedCollaborationRole)
      ? selectedCollaborationRole
      : "planner";

  useEffect(() => {
    void refreshConversations(initialConversationId || undefined);
    void refreshAgents();
    void toolCatalog.refresh();
    void knowledge.documents.refreshDocuments();
  }, [initialConversationId]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const controller = new AbortController();
    void getAPIHealth(controller.signal)
      .then(() => setAPIConnectionStatus("connected"))
      .catch(() => {
        if (!controller.signal.aborted) setAPIConnectionStatus("unavailable");
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const container = messagesRef.current;
    if (!container) {
      return;
    }
    container.scrollTo({ top: container.scrollHeight, behavior: "smooth" });
  }, [messages]);

  useEffect(() => () => {
    conversationRequests.cancel();
    streamRequests.cancel();
  }, [conversationRequests, streamRequests]);

  useEffect(() => {
    const runID = runState?.id;
    const status = runState?.status;
    if (!activeId || view !== "chat" || isStreaming || isContinuingRun || isResumingRun || !runID ||
      (status !== "queued" && status !== "running" && status !== "canceling")) {
      return;
    }

    const controller = new AbortController();
    const targetRevision = navigationRevision.current;
    const isCurrent = () => !controller.signal.aborted && targetRevision === navigationRevision.current;
    let refreshed = false;
    const refreshStoppedRun = () => {
      if (isCurrent() && !refreshed) {
        refreshed = true;
        void refreshConversations(activeId);
      }
    };
    const handleObservedEvent = createRunEventHandler({
      assistantDraftId: "",
      defaultVerificationStatus: runState.verificationStatus,
      fallbackAgentId: runState.agentId,
      fallbackRunId: runID,
      onRunState: (event) => {
        if (isStoppedRunStatus(event.status)) refreshStoppedRun();
      },
      setAutonomousProgress,
      setCollaborationSteps,
      setError,
      setIsCancelingRun,
      setMessages,
      setPlanDraft,
      setRunState
    });

    void observeRunEvents(runID, {
      signal: controller.signal,
      onEvent: (event, _sequence, replayed) => {
        if (!isCurrent()) return;
        // The canonical snapshot owns current Run status; historical lifecycle
        // events still rebuild Stage details but must not regress that status.
        if (!replayed || event.type !== "run_state") handleObservedEvent(event);
      },
      onSnapshot: (snapshot) => {
        if (!isCurrent()) return;
        if (snapshot.run.conversation_id !== activeId) return;
        setRunState({
          id: snapshot.run.run_id,
          agentId: runState.agentId,
          status: snapshot.run.status,
          verificationStatus: snapshot.run.verification_status
        });
        if (isStoppedRunStatus(snapshot.run.status)) refreshStoppedRun();
      }
    }).catch((err) => {
      if (isCurrent()) {
        setError(err instanceof Error ? `Live run updates unavailable: ${err.message}` : "Live run updates unavailable");
      }
    });

    return () => controller.abort();
  }, [activeId, isContinuingRun, isResumingRun, isStreaming, runState?.id, runState?.status, view]); // eslint-disable-line react-hooks/exhaustive-deps

  function handleChatModeChange(mode: ChatMode, preserveTaskState = false) {
    setChatMode(mode);
    setSidePanel((current) =>
      preserveTaskState && current === "task_state"
        ? current
        : mode === "multi_agent" || mode === "autonomous"
          ? "trace"
          : "closed"
    );
    if (mode !== "single") {
      closeAgentForms();
    }
  }

  async function refreshConversations(nextActiveId?: string) {
    const request = conversationRequests.begin();
    try {
      const items = await listConversations(request.signal);
      if (!request.isCurrent()) {
        return;
      }
      setConversations(items);
      if (nextActiveId) {
        setActiveId(nextActiveId);
        await loadConversation(nextActiveId, request);
        return;
      }
      if (!activeId && items[0]) {
        setActiveId(items[0].id);
        await loadConversation(items[0].id, request);
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
    setIsTaskStateLoading(true);
    try {
      const [messagesResult, taskStateResult] = await Promise.allSettled([
        listMessages(conversationId, request.signal),
        getTaskState(conversationId, request.signal)
      ]);
      if (!request.isCurrent()) {
        return;
      }
      if (messagesResult.status === "rejected") {
        throw messagesResult.reason;
      }
      setMessages(messagesResult.value);
      if (taskStateResult.status === "fulfilled") {
        setTaskState(taskStateResult.value);
        setTaskStateError("");
      } else {
        setTaskState(null);
        setTaskStateError(taskStateResult.reason instanceof Error ? taskStateResult.reason.message : "Failed to load task state");
      }
      await refreshCollaborationSteps(conversationId, request);
    } catch (err) {
      if (!request.isCurrent()) {
        return;
      }
      setMessages([]);
      resetConversationRuntimeState();
      setError(err instanceof Error ? err.message : "Failed to load conversation");
    } finally {
      if (request.isCurrent()) {
        setIsTaskStateLoading(false);
      }
    }
  }

  async function refreshTaskState(
    conversationId = activeId,
    request: LatestRequestLease = conversationRequests.begin()
  ) {
    if (!conversationId) {
      setTaskState(null);
      setTaskStateError("");
      return;
    }
    setIsTaskStateLoading(true);
    setTaskStateError("");
    try {
      const loaded = await getTaskState(conversationId, request.signal);
      if (request.isCurrent()) {
        setTaskState(loaded);
      }
    } catch (err) {
      if (request.isCurrent()) {
        setTaskStateError(err instanceof Error ? err.message : "Failed to load task state");
      }
    } finally {
      if (request.isCurrent()) {
        setIsTaskStateLoading(false);
      }
    }
  }

  async function refreshCollaborationSteps(conversationId: string, request: LatestRequestLease) {
    try {
      const runs = await listRuns(request.signal);
      if (!request.isCurrent()) {
        return;
      }
      const run = runs.find((item) => item.conversation_id === conversationId);
      if (!run) {
        resetConversationRuntimeState();
        return;
      }
      setRunState({
        id: run.id,
        agentId: run.agent_id,
        status: run.status,
        verificationStatus: run.verification_status ?? "not_required"
      });
      const steps = await listCollaborationSteps(run.id, request.signal);
      if (!request.isCurrent()) {
        return;
      }
      setCollaborationSteps(steps.map(toCollaborationStepView));
      if (steps.some((step) => autonomousRoles.some((role) => role.id === step.role))) {
        handleChatModeChange("autonomous", true);
      } else if (steps.length > 0) {
        handleChatModeChange("multi_agent", true);
      }
      const planner = steps.find((step) => step.role === "planner");
      setPlanDraft(planner?.output ?? "");
      const humanInput = steps.find((step) => step.role === "human_input" && step.status === "running");
      setHumanInputDraft((current) => (humanInput ? current : ""));
    } catch (err) {
      if (request.isCurrent()) {
        resetConversationRuntimeState();
        setError(err instanceof Error ? `Failed to load run trace: ${err.message}` : "Failed to load run trace");
      }
    }
  }

  function resetConversationRuntimeState() {
    setRunState(null);
    resetRunTrace();
    setIsCancelingRun(false);
  }

  async function openConversation(id: string) {
    navigationRevision.current += 1;
    streamRequests.cancel();
    setIsStreaming(false);
    setIsContinuingRun(false);
    setIsResumingRun(false);
    setError("");
    resetConversationRuntimeState();
    setMessages([]);
    setTaskState(null);
    setTaskStateError("");
    setView("chat");
    setActiveId(id);
    await loadConversation(id);
  }

  async function startNewConversation() {
    navigationRevision.current += 1;
    streamRequests.cancel();
    setIsStreaming(false);
    setIsContinuingRun(false);
    setIsResumingRun(false);
    const request = conversationRequests.begin();
    setError("");
    setView("chat");
    try {
      const conversation = await createConversation("New conversation");
      if (!request.isCurrent()) {
        return;
      }
      setConversations((items) => [conversation, ...items]);
      resetConversationRuntimeState();
      setActiveId(conversation.id);
      setMessages([]);
      await refreshTaskState(conversation.id, request);
    } catch (err) {
      if (request.isCurrent()) {
        setError(err instanceof Error ? err.message : "Failed to create conversation");
      }
    }
  }

  async function handleDeleteConversation(conversationId: string) {
    if (isStreaming || isContinuingRun || isResumingRun) {
      return;
    }
    const confirmed = window.confirm("Delete this conversation? This cannot be undone.");
    if (!confirmed) {
      return;
    }

    setError("");
    const request = conversationRequests.begin();
    try {
      await deleteConversationApi(conversationId);
      if (!request.isCurrent()) {
        setConversations((items) => items.filter((item) => item.id !== conversationId));
        return;
      }
      const items = await listConversations(request.signal);
      if (!request.isCurrent()) {
        setConversations((current) => current.filter((item) => item.id !== conversationId));
        return;
      }
      setConversations(items);

      if (conversationId === activeId) {
        navigationRevision.current += 1;
        streamRequests.cancel();
        conversationRequests.cancel();
        const nextConversation = items[0];
        resetConversationRuntimeState();
        setMessages([]);
        setTaskState(null);
        setTaskStateError("");

        if (nextConversation) {
          setActiveId(nextConversation.id);
          setView("chat");
          await loadConversation(nextConversation.id);
        } else {
          setActiveId("");
          setView("chat");
          setSidePanel("closed");
        }
      }
    } catch (err) {
      if (request.isCurrent()) setError(err instanceof Error ? err.message : "Failed to delete conversation");
    }
  }

  function handleOpenCompletionVerification() {
    if (isStreaming) {
      return;
    }
    verification.open();
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const content = input.trim();
    if (!content || isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput) {
      return;
    }
    let completionContract;
    try {
      completionContract = buildCompletionContract(verification.settings);
    } catch (contractError) {
      setError(contractError instanceof Error ? contractError.message : "Invalid verification policy");
      return;
    }

    const previousRuntime = {
      runState, collaborationSteps, autonomousProgress, humanInputDraft,
      planDraft, routingRequirements, selectedCollaborationRole, sidePanel
    };
    setInput("");
    setError("");
    setRunState(null);
    resetRunTrace();
    setIsCancelingRun(false);
    setSidePanel(chatMode === "multi_agent" || chatMode === "autonomous" ? "trace" : "closed");
    setIsStreaming(true);

    const optimisticUser: DraftMessage = {
      id: `local-user-${Date.now()}`,
      conversation_id: activeId,
      role: "user",
      content,
      created_at: new Date().toISOString()
    };
    const assistantDraft: DraftMessage = {
      id: `local-assistant-${Date.now()}`,
      conversation_id: activeId,
      role: "assistant",
      content: "",
      created_at: new Date().toISOString()
    };
    setMessages((items) => [...items, optimisticUser, assistantDraft]);

    let conversationId = activeId;
    let receivedStreamEvent = false;
    const request = streamRequests.begin();
    const handleEvent = createRunEventHandler({
      assistantDraftId: assistantDraft.id,
      defaultVerificationStatus: completionContract ? "pending" : "not_required",
      fallbackAgentId: activeAgentId,
      fallbackRunId: "",
      onConversation: (event) => {
        conversationId = event.conversation_id;
        conversationRequests.cancel();
        setActiveId(event.conversation_id);
      },
      onDone: (event) => applyTitle(event.conversation_id, event.title),
      onEvent: () => { receivedStreamEvent = true; },
      setAutonomousProgress,
      setCollaborationSteps,
      setError,
      setIsCancelingRun,
      setMessages,
      setPlanDraft,
      setRunState
    });

    try {
      await streamChat(
        {
          conversation_id: conversationId || undefined,
          agent_id: activeAgentId || undefined,
          mode: chatMode,
          message: content,
          completion_contract: completionContract
        },
        (event) => {
          if (request.isCurrent()) handleEvent(event);
        }
      );

      if (request.isCurrent()) await refreshConversations(conversationId);
    } catch (err) {
      if (!request.isCurrent()) return;
      if (!receivedStreamEvent) {
        setMessages((items) => removePendingMessages(items, [optimisticUser.id, assistantDraft.id]));
        setInput(content);
        setRunState(previousRuntime.runState);
        setCollaborationSteps(previousRuntime.collaborationSteps);
        setAutonomousProgress(previousRuntime.autonomousProgress);
        setHumanInputDraft(previousRuntime.humanInputDraft);
        setPlanDraft(previousRuntime.planDraft);
        setRoutingRequirements(previousRuntime.routingRequirements);
        setSelectedCollaborationRole(previousRuntime.selectedCollaborationRole);
        setSidePanel(previousRuntime.sidePanel);
      }
      setError(err instanceof Error ? err.message : "Unexpected chat error");
    } finally {
      if (request.isCurrent()) setIsStreaming(false);
    }

  }

  async function handleContinuePlan(planOverride?: string, requirementsOverride?: AgentRoutingRequirements) {
    const runID = runState?.id;
    const plan = (planOverride ?? planDraft).trim();
    if (!runID || !plan || isContinuingRun || isStreaming) {
      return;
    }

    setError("");
    setPlanDraft(plan);
    setIsContinuingRun(true);
    setIsStreaming(true);

    const assistantDraft: DraftMessage = {
      id: `local-assistant-${Date.now()}`,
      conversation_id: activeId,
      role: "assistant",
      content: "",
      created_at: new Date().toISOString()
    };
    setMessages((items) => [...items, assistantDraft]);
    let receivedStreamEvent = false;
    const request = streamRequests.begin();
    const handleEvent = createRunEventHandler({
      assistantDraftId: assistantDraft.id,
      defaultVerificationStatus: "not_required",
      fallbackAgentId: activeAgentId,
      fallbackRunId: runID,
      onEvent: () => { receivedStreamEvent = true; },
      setAutonomousProgress,
      setCollaborationSteps,
      setError,
      setIsCancelingRun,
      setMessages,
      setPlanDraft,
      setRunState
    });

    try {
      await continueRun(
        { run_id: runID, plan, routing_requirements: requirementsOverride ?? routingRequirements },
        (event) => { if (request.isCurrent()) handleEvent(event); }
      );

      if (request.isCurrent() && activeId) {
        await loadConversation(activeId);
      }
    } catch (err) {
      if (!request.isCurrent()) return;
      if (!receivedStreamEvent) {
        setMessages((items) => removePendingMessages(items, [assistantDraft.id]));
      }
      setError(err instanceof Error ? err.message : "Unexpected continue error");
    } finally {
      if (request.isCurrent()) {
        setIsContinuingRun(false);
        setIsStreaming(false);
      }
    }
  }

  async function handleResumeAutonomous(userInputOverride?: string) {
    const runID = runState?.id;
    const userInput = (userInputOverride ?? humanInputDraft).trim();
    if (!runID || !userInput || isResumingRun || isStreaming) {
      return;
    }

    setError("");
    setHumanInputDraft(userInput);
    setIsResumingRun(true);
    setIsStreaming(true);
    const previousRunState = runState;
    setRunState((current) =>
      current
        ? {
            ...current,
            status: "running"
          }
        : current
    );

    const assistantDraft: DraftMessage = {
      id: `local-assistant-${Date.now()}`,
      conversation_id: activeId,
      role: "assistant",
      content: "",
      created_at: new Date().toISOString()
    };
    setMessages((items) => [...items, assistantDraft]);
    let receivedStreamEvent = false;
    const request = streamRequests.begin();
    const handleEvent = createRunEventHandler({
      assistantDraftId: assistantDraft.id,
      defaultVerificationStatus: "not_required",
      fallbackAgentId: activeAgentId,
      fallbackRunId: runID,
      onEvent: () => { receivedStreamEvent = true; },
      onRunState: (event) => {
        if (event.status !== "waiting_for_user") setHumanInputDraft("");
      },
      setAutonomousProgress,
      setCollaborationSteps,
      setError,
      setIsCancelingRun,
      setMessages,
      setPlanDraft,
      setRunState
    });

    try {
      await resumeRun(
        { run_id: runID, user_input: userInput },
        (event) => { if (request.isCurrent()) handleEvent(event); }
      );

      if (request.isCurrent() && activeId) {
        await loadConversation(activeId);
      }
    } catch (err) {
      if (!request.isCurrent()) return;
      if (!receivedStreamEvent) {
        setMessages((items) => removePendingMessages(items, [assistantDraft.id]));
        setRunState(previousRunState);
      }
      setError(err instanceof Error ? err.message : "Unexpected resume error");
    } finally {
      if (request.isCurrent()) {
        setIsResumingRun(false);
        setIsStreaming(false);
      }
    }
  }

  async function handleCancelRun() {
    const runID = runState?.id;
    if (!runID || isCancelingRun) {
      return;
    }
    setError("");
    setIsCancelingRun(true);
    const targetRevision = navigationRevision.current;
    try {
      const canceled = await cancelRun(runID);
      if (targetRevision !== navigationRevision.current || currentRunId.current !== runID) return;
      setRunState({
        id: canceled.id,
        agentId: canceled.agent_id,
        status: canceled.status,
        verificationStatus: canceled.verification_status ?? "not_required"
      });
      if (
        canceled.status === "canceled" ||
        canceled.status === "completed" ||
        canceled.status === "failed" ||
        canceled.status === "failed_recoverable"
      ) {
        setIsCancelingRun(false);
      }
    } catch (err) {
      if (targetRevision !== navigationRevision.current || currentRunId.current !== runID) return;
      setError(err instanceof Error ? err.message : "Failed to cancel run");
      setIsCancelingRun(false);
    }
  }

  return (
    <div className={`shell ${isSidebarCollapsed ? "sidebar-collapsed" : ""}`}>
      <Sidebar
        activeId={activeId}
        apiConnectionStatus={apiConnectionStatus}
        conversations={conversations}
        isBusy={isStreaming || isContinuingRun}
        isCollapsed={isSidebarCollapsed}
        isOpen={isSidebarOpen}
        onCollapseChange={() => setIsSidebarCollapsed((current) => !current)}
        onDeleteConversation={(id) => void handleDeleteConversation(id)}
        onNewConversation={() => void startNewConversation()}
        onOpenConversation={(id) => void openConversation(id)}
        onOpenChange={setIsSidebarOpen}
        onViewChange={setView}
        onViewRefresh={(nextView) => {
          if (nextView === "tools") void toolCatalog.refresh();
          if (nextView === "knowledge") void knowledge.documents.refreshDocuments();
        }}
        view={view}
      />

      <main className="main">
        <Topbar
          activeConversation={activeConversation}
          canCancelRun={canCancelRun}
          conversationTitleDraft={conversationTitleDraft}
          documentCount={knowledge.documents.documents.length}
          editingConversationId={editingConversationId}
          isCancelingRun={isCancelingRun}
          isRunStreaming={isRunStreaming}
          isSavingConversationTitle={isSavingConversationTitle}
          memoryStatus={memory.hasSearched ? `${memory.results.length} matches` : "Recall ready"}
          onCancelEdit={cancelEditingTitle}
          onCancelRun={() => void handleCancelRun()}
          onConversationTitleDraftChange={setConversationTitleDraft}
          onOpenNavigation={() => setIsSidebarOpen(true)}
          onTaskStateToggle={() => setSidePanel((current) => current === "task_state" ? "closed" : "task_state")}
          onSaveTitle={() => void saveTitle()}
          onStartEdit={startEditingTitle}
          runState={runState}
          taskStateOpen={isTaskStatePanelOpen}
          taskStateVersion={taskState?.version ?? 0}
          toolCount={toolCatalog.tools.filter((tool) => tool.enabled).length}
          view={view}
        />

        {view === "tools" ? (
          <ToolsPanel error={toolCatalog.error} onToggle={(tool) => void toolCatalog.toggle(tool)} tools={toolCatalog.tools} updatingTool={toolCatalog.updatingTool} />
        ) : view === "knowledge" ? (
          <KnowledgePanel model={knowledge} />
        ) : view === "memory" ? (
          <MemoryPanel model={memory} />
        ) : view === "attention" ? (
          <OperatorAttentionPanel />
        ) : (
          <ChatWorkspace
            agents={agents}
            autonomousProgress={autonomousProgress}
            chatMode={chatMode}
            collaborationSteps={collaborationSteps}
            humanInputDraft={humanInputDraft}
            isCanceling={isCancelingRun || runState?.status === "canceling"}
            isCollaborationPanelOpen={isCollaborationPanelOpen}
            isContinuing={isContinuingRun}
            isResuming={isResumingRun}
            isStreaming={isStreaming}
            isTaskStatePanelOpen={isTaskStatePanelOpen}
            messages={messages}
            messagesRef={messagesRef}
            onCancel={() => void handleCancelRun()}
            onContinue={handleContinuePlan}
            onHumanInputChange={setHumanInputDraft}
            onModeChange={handleChatModeChange}
            onPanelOpenChange={(open) => setSidePanel(open ? "trace" : "closed")}
            onPlanDraftChange={setPlanDraft}
            onPromptSelect={setInput}
            onResume={handleResumeAutonomous}
            onRoleSelect={setSelectedCollaborationRole}
            onRoutingRequirementsChange={setRoutingRequirements}
            onTaskStateClose={() => setSidePanel("closed")}
            onTaskStateRefresh={() => void refreshTaskState()}
            planDraft={planDraft}
            routingRequirements={routingRequirements}
            runStatus={runState?.status ?? ""}
            selectedRole={visibleCollaborationRole}
            showAutonomousTrace={showAutonomousTrace}
            showCollaborationDag={showCollaborationDag}
            showCollaborationPanel={showCollaborationPanel}
            taskState={taskState}
            taskStateError={taskStateError}
            taskStateLoading={isTaskStateLoading}
            useExpandedConversationWidth={useExpandedConversationWidth}
          />
        )}

        {view === "chat" ? (
          <ChatComposer
            activeAgent={activeAgent}
            activeAgentId={activeAgentId}
            agents={agents}
            agentsError={agentsError}
            chatMode={chatMode}
            completionVerificationEnabled={verification.settings.enabled}
            error={error}
            input={input}
            isAgentDescriptionExpanded={isAgentDescriptionExpanded}
            isAwaitingHumanInput={isAwaitingHumanInput}
            isAwaitingPlanApproval={isAwaitingPlanApproval}
            isCreatingAgent={isCreatingAgent}
            isNewAgentFormOpen={isNewAgentFormOpen}
            isStreaming={isStreaming}
            onAgentChange={selectAgent}
            onConfigureAgent={openAgentConfig}
            onDescriptionExpandedChange={toggleDescription}
            onInputChange={setInput}
            onNewAgent={handleOpenNewAgentForm}
            onOpenVerification={handleOpenCompletionVerification}
            onSubmit={handleSubmit}
            showAgentActions={Boolean(activeAgent && agentConfigDraft)}
          />
        ) : null}
      </main>
      <ChatDialogs
        activeAgent={activeAgent}
        agentArchiveCandidate={agentArchiveCandidate}
        agentConfigDraft={agentConfigDraft}
        agentConfigStatus={agentConfigStatus}
        agentOperationNotice={agentOperationNotice}
        archivingAgentId={archivingAgentId}
        chatMode={chatMode}
        completionVerificationDraft={verification.draft}
        completionVerificationError={verification.error}
        isAgentConfigOpen={isAgentConfigOpen}
        isCreatingAgent={isCreatingAgent}
        isNewAgentFormOpen={isNewAgentFormOpen}
        isSavingAgentConfig={isSavingAgentConfig}
        isStreaming={isStreaming}
        newAgentDraft={newAgentDraft}
        onArchiveAgent={handleArchiveAgent}
        onCancelAgentConfig={handleCancelAgentConfig}
        onCancelArchive={cancelArchiveAgent}
        onCancelNewAgent={handleCancelNewAgent}
        onCompletionVerificationCancel={verification.cancel}
        onCompletionVerificationChange={verification.change}
        onConfirmArchive={() => void confirmArchiveAgent()}
        onCreateAgent={() => void handleCreateAgent()}
        onDismissNotice={dismissNotice}
        onNewAgentChange={updateNewAgentDraft}
        onNewAgentToolToggle={toggleNewAgentTool}
        onSaveAgentConfig={() => void handleSaveAgentConfig()}
        onSaveCompletionVerification={verification.save}
        onUpdateAgentChange={updateAgentConfigDraft}
        onUpdateAgentToolToggle={toggleAgentConfigTool}
        tools={toolCatalog.tools}
      />
    </div>
  );
}

function isStoppedRunStatus(status: string) {
  return status === "waiting_for_user" || status === "completed" || status === "failed" ||
    status === "failed_recoverable" || status === "canceled";
}
