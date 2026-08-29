"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import {
  AgentInfo,
  ChatMode,
  Conversation,
  ToolInfo,
  TaskState,
  archiveAgent,
  cancelRun,
  continueRun,
  createConversation,
  createAgent,
  deleteConversation as deleteConversationApi,
  listCollaborationSteps,
  listAgents,
  listConversations,
  listRuns,
  listTools,
  listMessages,
  getTaskState,
  resumeRun,
  setToolEnabled,
  streamChat,
  updateAgent,
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
import { Sidebar, ToolsPanel, Topbar, type ChatView } from "./chat/ChatChrome";
import { ChatComposer } from "./chat/ChatComposer";
import { ChatDialogs, type AgentOperationNotice } from "./chat/ChatDialogs";
import { ChatWorkspace } from "./chat/ChatWorkspace";
import { createRunEventHandler, type DraftMessage, type RunState } from "./chat/runEventProjection";
import { isDefaultAgent, type AgentConfigDraft } from "./chat/AgentConfigPanel";
import { autonomousRoles, toCollaborationStepView, type AutonomousProgress, type CollaborationStepView } from "./chat/CollaborationPanels";
import { KnowledgePanel } from "./knowledge/KnowledgePanel";
import { useKnowledgeWorkbench } from "./knowledge/useKnowledgeWorkbench";

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
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [activeAgentId, setActiveAgentId] = useState("");
  const [isAgentDescriptionExpanded, setIsAgentDescriptionExpanded] = useState(false);
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
  const [isContinuingRun, setIsContinuingRun] = useState(false);
  const [isResumingRun, setIsResumingRun] = useState(false);
  const [isCancelingRun, setIsCancelingRun] = useState(false);
  const [agentsError, setAgentsError] = useState("");
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
  const [isAgentConfigOpen, setIsAgentConfigOpen] = useState(false);
  const [agentConfigDraft, setAgentConfigDraft] = useState<AgentConfigDraft | null>(null);
  const [newAgentDraft, setNewAgentDraft] = useState<AgentConfigDraft | null>(null);
  const [isNewAgentFormOpen, setIsNewAgentFormOpen] = useState(false);
  const [isSavingAgentConfig, setIsSavingAgentConfig] = useState(false);
  const [isCreatingAgent, setIsCreatingAgent] = useState(false);
  const [archivingAgentId, setArchivingAgentId] = useState("");
  const [agentArchiveCandidate, setAgentArchiveCandidate] = useState<AgentInfo | null>(null);
  const [agentOperationNotice, setAgentOperationNotice] = useState<AgentOperationNotice | null>(null);
  const [agentConfigStatus, setAgentConfigStatus] = useState("");
  const [editingConversationId, setEditingConversationId] = useState("");
  const [conversationTitleDraft, setConversationTitleDraft] = useState("");
  const [isSavingConversationTitle, setIsSavingConversationTitle] = useState(false);
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false);
  const messagesRef = useRef<HTMLElement | null>(null);
  const conversationRequestsRef = useRef<ReturnType<typeof createLatestRequestController> | null>(null);
  if (!conversationRequestsRef.current) {
    conversationRequestsRef.current = createLatestRequestController();
  }
  const conversationRequests = conversationRequestsRef.current;
  const knowledge = useKnowledgeWorkbench();

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId),
    [activeId, conversations]
  );
  const activeAgent = useMemo(
    () => agents.find((agent) => agent.id === activeAgentId),
    [activeAgentId, agents]
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
    const container = messagesRef.current;
    if (!container) {
      return;
    }
    container.scrollTo({ top: container.scrollHeight, behavior: "smooth" });
  }, [messages]);

  useEffect(() => () => conversationRequests.cancel(), [conversationRequests]);

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
      setIsAgentConfigOpen(false);
      setIsNewAgentFormOpen(false);
      setNewAgentDraft(null);
      setAgentConfigStatus("");
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
    } catch {
      if (request.isCurrent()) {
        resetConversationRuntimeState();
      }
    }
  }

  function resetConversationRuntimeState() {
    setRunState(null);
    setCollaborationSteps([]);
    setAutonomousProgress(null);
    setPlanDraft("");
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

  async function refreshAgents() {
    try {
      setAgentsError("");
      const items = await listAgents();
      setAgents(items);
      const nextAgent =
        items.find((agent) => agent.id === activeAgentId) ??
        items.find((agent) => agent.id === "agent_planner") ??
        items[0];
      setActiveAgentId(nextAgent?.id ?? "");
      setAgentConfigDraft(nextAgent ? agentToConfigDraft(nextAgent) : null);
    } catch (err) {
      setAgentsError(err instanceof Error ? err.message : "Failed to load agents");
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

  function updateAgentConfigDraft(update: Partial<AgentConfigDraft>) {
    setAgentConfigStatus("");
    setAgentConfigDraft((current) => (current ? { ...current, ...update } : current));
  }

  function updateNewAgentDraft(update: Partial<AgentConfigDraft>) {
    setAgentConfigStatus("");
    setNewAgentDraft((current) => (current ? { ...current, ...update } : current));
  }

  function toggleAgentConfigTool(toolName: string) {
    setAgentConfigStatus("");
    setAgentConfigDraft((current) => {
      if (!current) {
        return current;
      }
      const enabled = current.tools.includes(toolName);
      return {
        ...current,
        tools: enabled ? current.tools.filter((name) => name !== toolName) : [...current.tools, toolName]
      };
    });
  }

  function toggleNewAgentTool(toolName: string) {
    setAgentConfigStatus("");
    setNewAgentDraft((current) => {
      if (!current) {
        return current;
      }
      const enabled = current.tools.includes(toolName);
      return {
        ...current,
        tools: enabled ? current.tools.filter((name) => name !== toolName) : [...current.tools, toolName]
      };
    });
  }

  async function handleSaveAgentConfig() {
    if (!activeAgent || !agentConfigDraft || isSavingAgentConfig) {
      return;
    }
    setIsSavingAgentConfig(true);
    setAgentsError("");
    setAgentConfigStatus("");
    try {
      const updated = await updateAgent(activeAgent.id, agentConfigDraft);
      setAgents((items) => items.map((item) => (item.id === updated.id ? updated : item)));
      setAgentConfigDraft(agentToConfigDraft(updated));
      setAgentConfigStatus("Agent config saved.");
      setIsAgentConfigOpen(false);
      setAgentOperationNotice({
        title: "Agent config saved",
        message: `${updated.name} has been updated and will be used by new runs.`,
        tone: "success"
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to save agent config";
      setAgentsError(message);
      setAgentConfigStatus(message);
      setAgentOperationNotice({
        title: "Failed to save agent",
        message,
        tone: "error"
      });
    } finally {
      setIsSavingAgentConfig(false);
    }
  }

  function handleOpenNewAgentForm() {
    if (isCreatingAgent || isStreaming) {
      return;
    }
    const base = activeAgent ?? agents[0];
    setNewAgentDraft({
      name: base ? `Copy of ${base.name}` : "New Agent",
      description: base?.description ?? "",
      system_prompt: base?.system_prompt ?? "You are a helpful AgentFlow agent.",
      tools: base?.tools ?? [],
      memory_enabled: base?.memory_enabled ?? true,
      retrieval_enabled: base?.retrieval_enabled ?? true
    });
    setIsNewAgentFormOpen(true);
    setIsAgentConfigOpen(false);
    setAgentConfigStatus("Fill out the form, then click Create Agent.");
  }

  function handleCancelNewAgent() {
    setIsNewAgentFormOpen(false);
    setNewAgentDraft(null);
    setAgentConfigStatus("");
  }

  function handleCancelAgentConfig() {
    if (isSavingAgentConfig) {
      return;
    }
    setIsAgentConfigOpen(false);
    setAgentConfigStatus("");
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

  async function handleCreateAgent() {
    if (isCreatingAgent || isStreaming || !newAgentDraft) {
      return;
    }
    setIsCreatingAgent(true);
    setAgentsError("");
    setAgentConfigStatus("");
    try {
      const created = await createAgent(newAgentDraft);
      setAgents((items) => [...items, created]);
      setActiveAgentId(created.id);
      setAgentConfigDraft(agentToConfigDraft(created));
      setIsAgentConfigOpen(false);
      setIsNewAgentFormOpen(false);
      setNewAgentDraft(null);
      setRunState(null);
      setAgentConfigStatus("New agent created.");
      setAgentOperationNotice({
        title: "Agent created",
        message: `${created.name} is now available in the active agent list.`,
        tone: "success"
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to create agent";
      setAgentsError(message);
      setAgentConfigStatus(message);
      setAgentOperationNotice({
        title: "Failed to create agent",
        message,
        tone: "error"
      });
    } finally {
      setIsCreatingAgent(false);
    }
  }

  function handleArchiveAgent() {
    if (!activeAgent || isDefaultAgent(activeAgent) || archivingAgentId) {
      return;
    }
    setAgentArchiveCandidate(activeAgent);
  }

  async function confirmArchiveAgent() {
    const candidate = agentArchiveCandidate;
    if (!candidate || isDefaultAgent(candidate) || archivingAgentId) {
      return;
    }
    setArchivingAgentId(candidate.id);
    setAgentsError("");
    setAgentConfigStatus("");
    try {
      await archiveAgent(candidate.id);
      const remaining = agents.filter((agent) => agent.id !== candidate.id);
      setAgents(remaining);
      const next = remaining.find((agent) => agent.id === "agent_planner") ?? remaining[0];
      setActiveAgentId(next?.id ?? "");
      setAgentConfigDraft(next ? agentToConfigDraft(next) : null);
      setIsAgentDescriptionExpanded(false);
      setIsAgentConfigOpen(false);
      setIsNewAgentFormOpen(false);
      setNewAgentDraft(null);
      setAgentArchiveCandidate(null);
      setRunState(null);
      setAgentConfigStatus("Agent archived.");
      setAgentOperationNotice({
        title: "Agent archived",
        message: `${candidate.name} was removed from the active agent list. Existing conversations and replay history remain available.`,
        tone: "success"
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to archive agent";
      setAgentsError(message);
      setAgentConfigStatus(message);
      setAgentArchiveCandidate(null);
      setAgentOperationNotice({
        title: "Failed to archive agent",
        message,
        tone: "error"
      });
    } finally {
      setArchivingAgentId("");
    }
  }

  function cancelArchiveAgent() {
    if (!archivingAgentId) {
      setAgentArchiveCandidate(null);
    }
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
      setError(err instanceof Error ? err.message : "Unexpected chat error");
    } finally {
      setIsStreaming(false);
    }
  }

  async function handleContinuePlan(planOverride?: string) {
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

    try {
      await continueRun(
        { run_id: runID, plan },
        createRunEventHandler({
          assistantDraftId: assistantDraft.id,
          defaultVerificationStatus: "not_required",
          fallbackAgentId: activeAgentId,
          fallbackRunId: runID,
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

    try {
      await resumeRun(
        { run_id: runID, user_input: userInput },
        createRunEventHandler({
          assistantDraftId: assistantDraft.id,
          defaultVerificationStatus: "not_required",
          fallbackAgentId: activeAgentId,
          fallbackRunId: runID,
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
            onTaskStateClose={() => setSidePanel("closed")}
            onTaskStateRefresh={() => void refreshTaskState()}
            planDraft={planDraft}
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
            onAgentChange={(agentId) => {
              const nextAgent = agents.find((agent) => agent.id === agentId);
              setActiveAgentId(agentId);
              setAgentConfigDraft(nextAgent ? agentToConfigDraft(nextAgent) : null);
              setIsAgentDescriptionExpanded(false);
              setRunState(null);
            }}
            onConfigureAgent={() => {
              setIsNewAgentFormOpen(false);
              setNewAgentDraft(null);
              setAgentConfigStatus("");
              setIsAgentConfigOpen(true);
            }}
            onDescriptionExpandedChange={() => setIsAgentDescriptionExpanded((current) => !current)}
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
        onDismissNotice={() => setAgentOperationNotice(null)}
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


function agentToConfigDraft(agent: AgentInfo): AgentConfigDraft {
  return {
    name: agent.name,
    description: agent.description,
    system_prompt: agent.system_prompt,
    tools: agent.tools ?? [],
    memory_enabled: agent.memory_enabled ?? true,
    retrieval_enabled: agent.retrieval_enabled ?? true
  };
}
