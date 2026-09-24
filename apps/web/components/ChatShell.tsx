"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import {
  AgentRoutingRequirements,
  ChatMode,
  Conversation,
  ToolInfo,
  TaskState,
  cancelRun,
  continueRun,
  createConversation,
  deleteConversation as deleteConversationApi,
  getAPIHealth,
  listCollaborationSteps,
  listConversations,
  listRuns,
  listTools,
  listMessages,
  getTaskState,
  observeRunEvents,
  resumeRun,
  setToolEnabled,
  streamChat,
  updateConversationTitle
} from "../lib/api";
import {
  DEFAULT_COMPLETION_VERIFICATION,
  buildCompletionContract,
  normalizeCompletionVerification,
  validateCompletionVerification
} from "../lib/verification";
import type { CompletionVerificationSettings } from "../lib/verification";
import { createLatestRequestController, type LatestRequestLease } from "../lib/latest-request";
import { removePendingMessages } from "../lib/pending-messages";
import { Sidebar, ToolsPanel, Topbar, type APIConnectionStatus, type ChatView } from "./chat/ChatChrome";
import { ChatComposer } from "./chat/ChatComposer";
import { ChatDialogs } from "./chat/ChatDialogs";
import { ChatWorkspace } from "./chat/ChatWorkspace";
import { createRunEventHandler, type DraftMessage, type RunState } from "./chat/runEventProjection";
import { useAgentManagement } from "./chat/useAgentManagement";
import { autonomousRoles, toCollaborationStepView, type AutonomousProgress, type CollaborationStepView } from "./chat/CollaborationPanels";
import { KnowledgePanel } from "./knowledge/KnowledgePanel";
import { useKnowledgeWorkbench } from "./knowledge/useKnowledgeWorkbench";
import { MemoryPanel } from "./memory/MemoryPanel";
import { useMemoryWorkbench } from "./memory/useMemoryWorkbench";
import { OperatorAttentionPanel } from "./attention/OperatorAttentionPanel";

type ChatShellProps = {
  initialConversationId?: string;
};

type ChatSidePanel = "trace" | "task_state" | "closed";

export function ChatShell({ initialConversationId = "" }: ChatShellProps) {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeId, setActiveId] = useState<string>("");
  const [messages, setMessages] = useState<DraftMessage[]>([]);
  const [input, setInput] = useState("");
  const [isStreaming, setIsStreaming] = useState(false);
  const [error, setError] = useState("");
  const [chatMode, setChatMode] = useState<ChatMode>("multi_agent");
  const [collaborationSteps, setCollaborationSteps] = useState<CollaborationStepView[]>([]);
  const [autonomousProgress, setAutonomousProgress] = useState<AutonomousProgress | null>(null);
  const [humanInputDraft, setHumanInputDraft] = useState("");
  const [sidePanel, setSidePanel] = useState<ChatSidePanel>("trace");
  const [taskState, setTaskState] = useState<TaskState | null>(null);
  const [taskStateError, setTaskStateError] = useState("");
  const [isTaskStateLoading, setIsTaskStateLoading] = useState(false);
  const [selectedCollaborationRole, setSelectedCollaborationRole] = useState("planner");
  const [planDraft, setPlanDraft] = useState("");
  const [routingRequirements, setRoutingRequirements] = useState<AgentRoutingRequirements>(emptyRoutingRequirements);
  const [isContinuingRun, setIsContinuingRun] = useState(false);
  const [isResumingRun, setIsResumingRun] = useState(false);
  const [isCancelingRun, setIsCancelingRun] = useState(false);
  const [runState, setRunState] = useState<RunState | null>(null);
  const [completionVerification, setCompletionVerification] = useState<CompletionVerificationSettings>(
    DEFAULT_COMPLETION_VERIFICATION
  );
  const [completionVerificationDraft, setCompletionVerificationDraft] =
    useState<CompletionVerificationSettings | null>(null);
  const [completionVerificationError, setCompletionVerificationError] = useState("");
  const [view, setView] = useState<ChatView>("chat");
  const [tools, setTools] = useState<ToolInfo[]>([]);
  const [toolsError, setToolsError] = useState("");
  const [updatingTool, setUpdatingTool] = useState("");
  const [editingConversationId, setEditingConversationId] = useState("");
  const [conversationTitleDraft, setConversationTitleDraft] = useState("");
  const [isSavingConversationTitle, setIsSavingConversationTitle] = useState(false);
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false);
  const [apiConnectionStatus, setAPIConnectionStatus] = useState<APIConnectionStatus>("checking");
  const messagesRef = useRef<HTMLElement | null>(null);
  const conversationRequestsRef = useRef<ReturnType<typeof createLatestRequestController> | null>(null);
  if (!conversationRequestsRef.current) {
    conversationRequestsRef.current = createLatestRequestController();
  }
  const conversationRequests = conversationRequestsRef.current;
  const knowledge = useKnowledgeWorkbench();
  const memory = useMemoryWorkbench();
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

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId),
    [activeId, conversations]
  );
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
    void refreshTools();
    void knowledge.refreshDocuments();
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

  useEffect(() => () => conversationRequests.cancel(), [conversationRequests]);

  useEffect(() => {
    const runID = runState?.id;
    const status = runState?.status;
    if (!activeId || view !== "chat" || isStreaming || isContinuingRun || isResumingRun || !runID ||
      (status !== "queued" && status !== "running" && status !== "canceling")) {
      return;
    }

    const controller = new AbortController();
    let refreshed = false;
    const refreshStoppedRun = () => {
      if (!refreshed) {
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
        // The canonical snapshot owns current Run status; historical lifecycle
        // events still rebuild Stage details but must not regress that status.
        if (!replayed || event.type !== "run_state") handleObservedEvent(event);
      },
      onSnapshot: (snapshot) => {
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
      if (!controller.signal.aborted) {
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
    setCollaborationSteps([]);
    setAutonomousProgress(null);
    setPlanDraft("");
    setRoutingRequirements(emptyRoutingRequirements());
    setHumanInputDraft("");
    setIsCancelingRun(false);
  }

  async function refreshTools() {
    try {
      setToolsError("");
      setTools(await listTools());
    } catch (err) {
      setToolsError(err instanceof Error ? err.message : "Failed to load tools");
    }
  }

  async function openConversation(id: string) {
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
    const request = conversationRequests.begin();
    setError("");
    resetConversationRuntimeState();
    setView("chat");
    try {
      const conversation = await createConversation("New conversation");
      if (!request.isCurrent()) {
        return;
      }
      setConversations((items) => [conversation, ...items]);
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
    if (isStreaming || isContinuingRun) {
      return;
    }
    const confirmed = window.confirm("Delete this conversation? This cannot be undone.");
    if (!confirmed) {
      return;
    }

    setError("");
    try {
      await deleteConversationApi(conversationId);
      const items = await listConversations();
      setConversations(items);

      if (conversationId === activeId) {
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
      setError(err instanceof Error ? err.message : "Failed to delete conversation");
    }
  }

  function startEditingConversationTitle(conversation: Conversation) {
    setEditingConversationId(conversation.id);
    setConversationTitleDraft(conversation.title);
    setError("");
  }

  function cancelEditingConversationTitle() {
    setEditingConversationId("");
    setConversationTitleDraft("");
  }

  async function saveConversationTitle() {
    const conversationId = editingConversationId;
    const title = conversationTitleDraft.trim();
    if (!conversationId || !title || isSavingConversationTitle) {
      return;
    }
    setIsSavingConversationTitle(true);
    setError("");
    try {
      const updated = await updateConversationTitle(conversationId, title);
      setConversations((items) => items.map((item) => (item.id === updated.id ? updated : item)));
      cancelEditingConversationTitle();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to update conversation title");
    } finally {
      setIsSavingConversationTitle(false);
    }
  }

  function applyConversationTitle(conversationId: string, title?: string) {
    const trimmed = title?.trim();
    if (!trimmed) {
      return;
    }
    setConversations((items) =>
      items.map((item) =>
        item.id === conversationId ? { ...item, title: trimmed, updated_at: new Date().toISOString() } : item
      )
    );
  }

  async function toggleTool(tool: ToolInfo) {
    setUpdatingTool(tool.name);
    setToolsError("");
    try {
      setTools(await setToolEnabled(tool.name, !tool.enabled));
    } catch (err) {
      setToolsError(err instanceof Error ? err.message : "Failed to update tool");
    } finally {
      setUpdatingTool("");
    }
  }

  function handleOpenCompletionVerification() {
    if (isStreaming) {
      return;
    }
    setCompletionVerificationError("");
    setCompletionVerificationDraft(structuredClone(completionVerification));
  }

  function handleSaveCompletionVerification() {
    if (!completionVerificationDraft) {
      return;
    }
    const normalized = normalizeCompletionVerification(completionVerificationDraft);
    const validationErrors = validateCompletionVerification(normalized);
    if (validationErrors.length > 0) {
      setCompletionVerificationError(validationErrors[0]);
      return;
    }
    setCompletionVerification(normalized);
    setCompletionVerificationError("");
    setCompletionVerificationDraft(null);
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const content = input.trim();
    if (!content || isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput) {
      return;
    }
    let completionContract;
    try {
      completionContract = buildCompletionContract(completionVerification);
    } catch (contractError) {
      setError(contractError instanceof Error ? contractError.message : "Invalid verification policy");
      return;
    }

    setInput("");
    setError("");
    setRunState(null);
    setCollaborationSteps([]);
    setAutonomousProgress(null);
    setHumanInputDraft("");
    setPlanDraft("");
    setRoutingRequirements(emptyRoutingRequirements());
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

    try {
      await streamChat(
        {
          conversation_id: conversationId || undefined,
          agent_id: activeAgentId || undefined,
          mode: chatMode,
          message: content,
          completion_contract: completionContract
        },
        createRunEventHandler({
          assistantDraftId: assistantDraft.id,
          defaultVerificationStatus: completionContract ? "pending" : "not_required",
          fallbackAgentId: activeAgentId,
          fallbackRunId: "",
          onConversation: (event) => {
            conversationId = event.conversation_id;
            conversationRequests.cancel();
            setActiveId(event.conversation_id);
          },
          onDone: (event) => applyConversationTitle(event.conversation_id, event.title),
          onEvent: () => { receivedStreamEvent = true; },
          setAutonomousProgress,
          setCollaborationSteps,
          setError,
          setIsCancelingRun,
          setMessages,
          setPlanDraft,
          setRunState
        })
      );

      await refreshConversations(conversationId);
    } catch (err) {
      if (!receivedStreamEvent) {
        setMessages((items) => removePendingMessages(items, [optimisticUser.id, assistantDraft.id]));
        setInput(content);
      }
      setError(err instanceof Error ? err.message : "Unexpected chat error");
    } finally {
      setIsStreaming(false);
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

    try {
      await continueRun(
        { run_id: runID, plan, routing_requirements: requirementsOverride ?? routingRequirements },
        createRunEventHandler({
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
        })
      );

      if (activeId) {
        await loadConversation(activeId);
      }
    } catch (err) {
      if (!receivedStreamEvent) {
        setMessages((items) => removePendingMessages(items, [assistantDraft.id]));
      }
      setError(err instanceof Error ? err.message : "Unexpected continue error");
    } finally {
      setIsContinuingRun(false);
      setIsStreaming(false);
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

    try {
      await resumeRun(
        { run_id: runID, user_input: userInput },
        createRunEventHandler({
          assistantDraftId: assistantDraft.id,
          defaultVerificationStatus: "not_required",
          fallbackAgentId: activeAgentId,
          fallbackRunId: runID,
          onEvent: () => { receivedStreamEvent = true; },
          onRunState: (event) => {
            if (event.status !== "waiting_for_user") {
              setHumanInputDraft("");
            }
          },
          setAutonomousProgress,
          setCollaborationSteps,
          setError,
          setIsCancelingRun,
          setMessages,
          setPlanDraft,
          setRunState
        })
      );

      if (activeId) {
        await loadConversation(activeId);
      }
    } catch (err) {
      if (!receivedStreamEvent) {
        setMessages((items) => removePendingMessages(items, [assistantDraft.id]));
        setRunState(previousRunState);
      }
      setError(err instanceof Error ? err.message : "Unexpected resume error");
    } finally {
      setIsResumingRun(false);
      setIsStreaming(false);
    }
  }

  async function handleCancelRun() {
    const runID = runState?.id;
    if (!runID || isCancelingRun) {
      return;
    }
    setError("");
    setIsCancelingRun(true);
    try {
      const canceled = await cancelRun(runID);
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
          if (nextView === "tools") void refreshTools();
          if (nextView === "knowledge") void knowledge.refreshDocuments();
        }}
        view={view}
      />

      <main className="main">
        <Topbar
          activeConversation={activeConversation}
          canCancelRun={canCancelRun}
          conversationTitleDraft={conversationTitleDraft}
          documentCount={knowledge.documents.length}
          editingConversationId={editingConversationId}
          isCancelingRun={isCancelingRun}
          isRunStreaming={isRunStreaming}
          isSavingConversationTitle={isSavingConversationTitle}
          memoryStatus={memory.hasSearched ? `${memory.results.length} matches` : "Recall ready"}
          onCancelEdit={cancelEditingConversationTitle}
          onCancelRun={() => void handleCancelRun()}
          onConversationTitleDraftChange={setConversationTitleDraft}
          onOpenNavigation={() => setIsSidebarOpen(true)}
          onTaskStateToggle={() => setSidePanel((current) => current === "task_state" ? "closed" : "task_state")}
          onSaveTitle={() => void saveConversationTitle()}
          onStartEdit={startEditingConversationTitle}
          runState={runState}
          taskStateOpen={isTaskStatePanelOpen}
          taskStateVersion={taskState?.version ?? 0}
          toolCount={tools.filter((tool) => tool.enabled).length}
          view={view}
        />

        {view === "tools" ? (
          <ToolsPanel error={toolsError} onToggle={(tool) => void toggleTool(tool)} tools={tools} updatingTool={updatingTool} />
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
            completionVerificationEnabled={completionVerification.enabled}
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
        completionVerificationDraft={completionVerificationDraft}
        completionVerificationError={completionVerificationError}
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
        onCompletionVerificationCancel={() => {
          setCompletionVerificationDraft(null);
          setCompletionVerificationError("");
        }}
        onCompletionVerificationChange={(update) => {
          setCompletionVerificationError("");
          setCompletionVerificationDraft((current) => (current ? { ...current, ...update } : current));
        }}
        onConfirmArchive={() => void confirmArchiveAgent()}
        onCreateAgent={() => void handleCreateAgent()}
        onDismissNotice={dismissNotice}
        onNewAgentChange={updateNewAgentDraft}
        onNewAgentToolToggle={toggleNewAgentTool}
        onSaveAgentConfig={() => void handleSaveAgentConfig()}
        onSaveCompletionVerification={handleSaveCompletionVerification}
        onUpdateAgentChange={updateAgentConfigDraft}
        onUpdateAgentToolToggle={toggleAgentConfigTool}
        tools={tools}
      />
    </div>
  );
}

function emptyRoutingRequirements(): AgentRoutingRequirements {
  return {
    required_tools: [],
    prohibited_tools: [],
    require_memory: false,
    require_retrieval: false,
    preferred_capabilities: []
  };
}

function isStoppedRunStatus(status: string) {
  return status === "waiting_for_user" || status === "completed" || status === "failed" ||
    status === "failed_recoverable" || status === "canceled";
}
