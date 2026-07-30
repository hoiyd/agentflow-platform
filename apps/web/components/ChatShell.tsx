"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import Link from "next/link";
import { lexer } from "marked";
import type { Token, Tokens } from "marked";
import {
  Activity,
  Bot,
  ChevronDown,
  ChevronUp,
  Database,
  GitBranch,
  Menu,
  MessageSquare,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Pencil,
  Plus,
  Repeat2,
  Send,
  Settings2,
  ShieldCheck,
  Square,
  Trash2,
  UserRoundPlus,
  Users,
  Wrench,
  X
} from "lucide-react";
import {
  AgentInfo,
  ChatExecutor,
  ChatMode,
  Conversation,
  Message,
  ToolInfo,
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
import { CollaborationDag } from "./CollaborationDag";
import { CompletionVerificationPanel } from "./CompletionVerificationPanel";
import { KnowledgePanel } from "./knowledge/KnowledgePanel";
import { useKnowledgeWorkbench } from "./knowledge/useKnowledgeWorkbench";

type DraftMessage = Pick<Message, "role" | "content" | "citations"> & {
  id: string;
  conversation_id: string;
  created_at: string;
};

type AgentConfigDraft = {
  name: string;
  description: string;
  system_prompt: string;
  tools: string[];
  memory_enabled: boolean;
  retrieval_enabled: boolean;
  executor: ChatExecutor;
};

type AgentOperationNotice = {
  title: string;
  message: string;
  tone: "success" | "error";
};

export type CollaborationStepView = {
  role: string;
  agent_id?: string;
  status: string;
  iteration?: number;
  input?: string;
  output?: string;
  error?: string;
};

export type CollaborationRole = {
  id: string;
  label: string;
  empty: string;
};

type AutonomousProgress = {
  iteration: number;
  maxIterations: number;
  elapsedSeconds: number;
  maxRuntimeSeconds: number;
  outputChars: number;
  maxOutputChars: number;
  toolCalls: number;
  maxToolCalls: number;
  stopReason?: string;
};

type ChatShellProps = {
  initialConversationId?: string;
};

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
  const [isCollaborationPanelOpen, setIsCollaborationPanelOpen] = useState(true);
  const [selectedCollaborationRole, setSelectedCollaborationRole] = useState("planner");
  const [planDraft, setPlanDraft] = useState("");
  const [isContinuingRun, setIsContinuingRun] = useState(false);
  const [isResumingRun, setIsResumingRun] = useState(false);
  const [isCancelingRun, setIsCancelingRun] = useState(false);
  const [agentsError, setAgentsError] = useState("");
  const [runState, setRunState] = useState<{
    id: string;
    agentId: string;
    status: string;
    verificationStatus: string;
  } | null>(null);
  const [completionVerification, setCompletionVerification] = useState<CompletionVerificationSettings>(
    DEFAULT_COMPLETION_VERIFICATION
  );
  const [completionVerificationDraft, setCompletionVerificationDraft] =
    useState<CompletionVerificationSettings | null>(null);
  const [completionVerificationError, setCompletionVerificationError] = useState("");
  const [view, setView] = useState<"chat" | "tools" | "knowledge">("chat");
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
  const useExpandedConversationWidth =
    chatMode === "single" || (showCollaborationPanel && !isCollaborationPanelOpen);
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

  function handleChatModeChange(mode: ChatMode) {
    setChatMode(mode);
    setIsCollaborationPanelOpen(mode === "multi_agent" || mode === "autonomous");
    if (mode !== "single") {
      setIsAgentConfigOpen(false);
      setIsNewAgentFormOpen(false);
      setNewAgentDraft(null);
      setAgentConfigStatus("");
    }
  }

  async function refreshConversations(nextActiveId?: string) {
    const items = await listConversations();
    setConversations(items);
    if (nextActiveId) {
      setActiveId(nextActiveId);
      await loadConversation(nextActiveId);
      return;
    }
    if (!activeId && items[0]) {
      setActiveId(items[0].id);
      await loadConversation(items[0].id);
    }
  }

  async function loadConversation(conversationId: string) {
    const loaded = await listMessages(conversationId);
    setMessages(loaded);
    await refreshCollaborationSteps(conversationId);
  }

  async function refreshCollaborationSteps(conversationId: string) {
    try {
      const runs = await listRuns();
      const run = runs.find((item) => item.conversation_id === conversationId);
      if (!run) {
        setRunState(null);
        setCollaborationSteps([]);
        setAutonomousProgress(null);
        setPlanDraft("");
        setHumanInputDraft("");
        setIsCancelingRun(false);
        return;
      }
      setRunState({
        id: run.id,
        agentId: run.agent_id,
        status: run.status,
        verificationStatus: run.verification_status ?? "not_required"
      });
      const steps = await listCollaborationSteps(run.id);
      setCollaborationSteps(steps.map(toCollaborationStepView));
      if (steps.some((step) => autonomousRoles.some((role) => role.id === step.role))) {
        handleChatModeChange("autonomous");
      } else if (steps.length > 0) {
        handleChatModeChange("multi_agent");
      }
      const planner = steps.find((step) => step.role === "planner");
      setPlanDraft(planner?.output ?? "");
      const humanInput = steps.find((step) => step.role === "human_input" && step.status === "running");
      setHumanInputDraft((current) => (humanInput ? current : ""));
    } catch {
      setRunState(null);
      setCollaborationSteps([]);
      setAutonomousProgress(null);
      setPlanDraft("");
      setHumanInputDraft("");
      setIsCancelingRun(false);
    }
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
    setRunState(null);
    setCollaborationSteps([]);
    setAutonomousProgress(null);
    setHumanInputDraft("");
    setPlanDraft("");
    setIsCancelingRun(false);
    setView("chat");
    setActiveId(id);
    await loadConversation(id);
  }

  async function startNewConversation() {
    setError("");
    setRunState(null);
    setCollaborationSteps([]);
    setAutonomousProgress(null);
    setHumanInputDraft("");
    setPlanDraft("");
    setIsCancelingRun(false);
    setView("chat");
    const conversation = await createConversation("New conversation");
    setConversations((items) => [conversation, ...items]);
    setActiveId(conversation.id);
    setMessages([]);
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
        const nextConversation = items[0];
        setRunState(null);
        setCollaborationSteps([]);
        setAutonomousProgress(null);
        setHumanInputDraft("");
        setPlanDraft("");
        setIsCancelingRun(false);
        setMessages([]);

        if (nextConversation) {
          setActiveId(nextConversation.id);
          setView("chat");
          await loadConversation(nextConversation.id);
        } else {
          setActiveId("");
          setView("chat");
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
      retrieval_enabled: base?.retrieval_enabled ?? true,
      executor: base?.executor ?? "native"
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
      setError(contractError instanceof Error ? contractError.message : "Invalid completion verification policy");
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
    if (chatMode === "multi_agent" || chatMode === "autonomous") {
      setIsCollaborationPanelOpen(true);
    }
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
        (event) => {
          if (event.type === "conversation") {
            conversationId = event.conversation_id;
            setActiveId(event.conversation_id);
          }
          if (event.type === "run_state") {
            setRunState((current) => ({
              id: event.run_id,
              agentId: event.agent_id,
              status: event.status,
              verificationStatus:
                current?.verificationStatus ?? (completionContract ? "pending" : "not_required")
            }));
            if (
              event.status === "canceled" ||
              event.status === "completed" ||
              event.status === "failed" ||
              event.status === "failed_recoverable"
            ) {
              setIsCancelingRun(false);
            }
          }
          if (event.type === "model_delta") {
            setMessages((items) =>
              items.map((item) =>
                item.id === assistantDraft.id ? { ...item, content: item.content + event.delta } : item
              )
            );
          }
          if (event.type === "stage_state") {
            setCollaborationSteps((items) => upsertCollaborationStep(items, event));
            if (event.role === "planner" && event.output) {
              setPlanDraft(event.output);
            }
          }
          if (event.type === "run_progress") {
            setAutonomousProgress(toAutonomousProgress(event));
          }
          if (event.type === "error") {
            setError(event.error);
          }
          if (event.type === "done") {
            setMessages((items) =>
              items.map((item) => item.id === assistantDraft.id ? { ...item, citations: event.citations } : item)
            );
            applyConversationTitle(event.conversation_id, event.title);
            setRunState((current) => ({
              id: event.run_id ?? current?.id ?? "",
              agentId: event.agent_id ?? current?.agentId ?? activeAgentId,
              status: event.status ?? "completed",
              verificationStatus:
                event.verification_status ?? current?.verificationStatus ?? (completionContract ? "pending" : "not_required")
            }));
            setIsCancelingRun(false);
          }
        }
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
      await continueRun({ run_id: runID, plan }, (event) => {
        if (event.type === "run_state") {
          setRunState((current) => ({
            id: event.run_id,
            agentId: event.agent_id,
            status: event.status,
            verificationStatus: current?.verificationStatus ?? "not_required"
          }));
        }
        if (event.type === "stage_state") {
          setCollaborationSteps((items) => upsertCollaborationStep(items, event));
          if (event.role === "planner" && event.output) {
            setPlanDraft(event.output);
          }
        }
        if (event.type === "model_delta") {
          setMessages((items) =>
            items.map((item) =>
              item.id === assistantDraft.id ? { ...item, content: item.content + event.delta } : item
            )
          );
        }
        if (event.type === "error") {
          setError(event.error);
        }
        if (event.type === "done") {
          setMessages((items) =>
            items.map((item) => item.id === assistantDraft.id ? { ...item, citations: event.citations } : item)
          );
          setRunState((current) => ({
            id: event.run_id ?? current?.id ?? runID,
            agentId: event.agent_id ?? current?.agentId ?? activeAgentId,
            status: event.status ?? "completed",
            verificationStatus: event.verification_status ?? current?.verificationStatus ?? "not_required"
          }));
        }
      });

      if (activeId) {
        const persisted = await listMessages(activeId);
        setMessages(persisted);
        await refreshCollaborationSteps(activeId);
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
      await resumeRun({ run_id: runID, user_input: userInput }, (event) => {
        if (event.type === "run_state") {
          setRunState((current) => ({
            id: event.run_id,
            agentId: event.agent_id,
            status: event.status,
            verificationStatus: current?.verificationStatus ?? "not_required"
          }));
          if (event.status !== "waiting_for_user") {
            setHumanInputDraft("");
          }
        }
        if (event.type === "stage_state") {
          setCollaborationSteps((items) => upsertCollaborationStep(items, event));
        }
        if (event.type === "run_progress") {
          setAutonomousProgress(toAutonomousProgress(event));
        }
        if (event.type === "model_delta") {
          setMessages((items) =>
            items.map((item) =>
              item.id === assistantDraft.id ? { ...item, content: item.content + event.delta } : item
            )
          );
        }
        if (event.type === "error") {
          setError(event.error);
        }
        if (event.type === "done") {
          setMessages((items) =>
            items.map((item) => item.id === assistantDraft.id ? { ...item, citations: event.citations } : item)
          );
          setRunState((current) => ({
            id: event.run_id ?? current?.id ?? runID,
            agentId: event.agent_id ?? current?.agentId ?? activeAgentId,
            status: event.status ?? "completed",
            verificationStatus: event.verification_status ?? current?.verificationStatus ?? "not_required"
          }));
        }
      });

      if (activeId) {
        const persisted = await listMessages(activeId);
        setMessages(persisted);
        await refreshCollaborationSteps(activeId);
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
      <aside className={`sidebar ${isSidebarOpen ? "mobile-open" : ""} ${isSidebarCollapsed ? "collapsed" : ""}`}>
        <div className="brand">
          <Link className="workspace-brand" href="/" title="AgentFlow Operations workspace">
            <span className="brand-mark" aria-hidden="true"><span /></span>
            <span><strong>AgentFlow</strong><small>Operations workspace</small></span>
          </Link>
          <button
            aria-label={isSidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="sidebar-collapse"
            onClick={() => setIsSidebarCollapsed((current) => !current)}
            title={isSidebarCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            type="button"
          >
            {isSidebarCollapsed ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
          </button>
          <button className="sidebar-close" aria-label="Close navigation" onClick={() => setIsSidebarOpen(false)} type="button">
            <X size={18} />
          </button>
        </div>
        <button className="new-chat" title="New conversation" onClick={() => {
          setIsSidebarOpen(false);
          void startNewConversation();
        }}>
          <Plus size={17} /> <span>New conversation</span>
        </button>
        <div className="sidebar-section">
          <div className="sidebar-section-title">Operate</div>
          <button
            className={`nav-button ${view === "chat" ? "active" : ""}`}
            title="Chat"
            onClick={() => {
              setView("chat");
              setIsSidebarOpen(false);
            }}
          >
            <MessageSquare size={16} /> <span>Chat</span>
          </button>
          <button
            className={`nav-button ${view === "tools" ? "active" : ""}`}
            title="Tools"
            onClick={() => {
              setView("tools");
              setIsSidebarOpen(false);
              void refreshTools();
            }}
          >
            <Wrench size={16} /> <span>Tools</span>
          </button>
          <button
            className={`nav-button ${view === "knowledge" ? "active" : ""}`}
            title="Knowledge"
            onClick={() => {
              setView("knowledge");
              setIsSidebarOpen(false);
              void knowledge.refreshDocuments();
            }}
          >
            <Database size={16} /> <span>Knowledge</span>
          </button>
        </div>
        <div className="sidebar-section-title conversation-section-title">Recent runs</div>
        <div className="conversation-list">
          {conversations.map((conversation) => {
            const isActiveConversation = conversation.id === activeId;
            return (
              <div
                className={`conversation-item ${isActiveConversation ? "active" : ""}`}
                key={conversation.id}
                role="button"
                tabIndex={0}
                onClick={() => {
                  setIsSidebarOpen(false);
                  void openConversation(conversation.id);
                }}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    void openConversation(conversation.id);
                  }
                }}
              >
                <div className="conversation-item-main">
                  <span className="conversation-title" title={conversation.title}>{conversation.title}</span>
                  <span className="conversation-date">
                    {new Date(conversation.updated_at).toLocaleDateString(undefined, {
                      month: "short",
                      day: "numeric"
                    })}
                  </span>
                </div>
                <button
                  aria-label={`Delete conversation ${conversation.title}`}
                  className="conversation-delete"
                  disabled={isStreaming || isContinuingRun}
                  onClick={(event) => {
                    event.stopPropagation();
                    void handleDeleteConversation(conversation.id);
                  }}
                  type="button"
                >
                  <Trash2 size={14} />
                </button>
              </div>
            );
          })}
        </div>
        <div className="sidebar-runtime">
          <span><i /> <span className="sidebar-runtime-label">API connected</span></span>
          <code>v0.7</code>
        </div>
      </aside>

      <button
        aria-label="Close navigation"
        className={`sidebar-scrim ${isSidebarOpen ? "visible" : ""}`}
        onClick={() => setIsSidebarOpen(false)}
        type="button"
      />

      <main className="main">
        <header className="topbar">
          <button className="mobile-menu" aria-label="Open navigation" onClick={() => setIsSidebarOpen(true)} type="button">
            <Menu size={19} />
          </button>
          {view === "chat" && activeConversation ? (
            editingConversationId === activeConversation.id ? (
              <form
                className="conversation-title-editor"
                onSubmit={(event) => {
                  event.preventDefault();
                  void saveConversationTitle();
                }}
              >
                <input
                  aria-label="Conversation title"
                  autoFocus
                  disabled={isSavingConversationTitle}
                  onChange={(event) => setConversationTitleDraft(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Escape") {
                      cancelEditingConversationTitle();
                    }
                  }}
                  value={conversationTitleDraft}
                />
                <button disabled={isSavingConversationTitle || !conversationTitleDraft.trim()} type="submit">
                  Save
                </button>
                <button disabled={isSavingConversationTitle} onClick={cancelEditingConversationTitle} type="button">
                  Cancel
                </button>
              </form>
            ) : (
              <div className="conversation-title-display">
                <div>
                  <span className="topbar-eyebrow">Conversation</span>
                  <h2>{activeConversation.title}</h2>
                </div>
                <button
                  aria-label="Rename conversation"
                  className="conversation-title-edit"
                  disabled={isSavingConversationTitle}
                  onClick={() => startEditingConversationTitle(activeConversation)}
                  type="button"
                >
                  <Pencil size={14} />
                </button>
              </div>
            )
          ) : (
            <div className="topbar-heading">
              <span className="topbar-eyebrow">Workspace</span>
              <h2>{view === "tools" ? "Tools" : view === "knowledge" ? "Knowledge" : "New conversation"}</h2>
            </div>
          )}
          <div className="topbar-actions">
            {view === "chat" && runState ? (
              <span
                aria-label={`Task status: ${runState.status.replaceAll("_", " ")}`}
                className={`run-status-indicator ${runState.status}`}
                title={`Run ${runState.id}`}
              >
                <i />
                <span>Task</span>
                <strong>{runState.status.replaceAll("_", " ")}</strong>
              </span>
            ) : null}
            {view === "chat" && runState && runState.verificationStatus !== "not_required" ? (
              <span
                aria-label={`Verification status: ${runState.verificationStatus.replaceAll("_", " ")}`}
                className={`run-status-indicator verification-status-indicator ${runState.verificationStatus}`}
              >
                <ShieldCheck size={13} />
                <span>Verification</span>
                <strong>{runState.verificationStatus.replaceAll("_", " ")}</strong>
              </span>
            ) : null}
            {view === "chat" && canCancelRun ? (
              <button
                className="topbar-run-stop"
                disabled={isCancelingRun || runState?.status === "canceling"}
                onClick={handleCancelRun}
                type="button"
              >
                <Square size={12} fill="currentColor" />
                {isCancelingRun || runState?.status === "canceling" ? "Stopping" : "Stop"}
              </button>
            ) : null}
            {view === "chat" && runState?.id ? (
              <a className="run-link" href={`/runs/${runState.id}`}>
                <Activity size={15} /> View trace
              </a>
            ) : null}
            {view !== "chat" || !runState ? (
              <span className={`status ${isRunStreaming ? "active" : ""}`}>
                <i />
                {view === "tools"
                  ? `${tools.filter((tool) => tool.enabled).length} enabled`
                  : view === "knowledge"
                    ? `${knowledge.documents.length} documents`
                    : isRunStreaming
                      ? "Streaming..."
                      : "Ready"}
              </span>
            ) : null}
          </div>
        </header>

        {view === "tools" ? (
          <section className="tools-panel">
            {toolsError ? <div className="error">{toolsError}</div> : null}
            <div className="tools-list">
              {tools.map((tool) => (
                <article className="tool-card" key={tool.name}>
                  <div className="tool-card-header">
                    <div>
                      <h3>{tool.name}</h3>
                    </div>
                    <label className="tool-toggle">
                      <input
                        type="checkbox"
                        checked={tool.enabled}
                        disabled={updatingTool === tool.name}
                        onChange={() => toggleTool(tool)}
                      />
                      <span>{tool.enabled ? "Enabled" : "Disabled"}</span>
                    </label>
                  </div>
                  <p>{tool.description}</p>
                  <pre>{formatValue(tool.parameters)}</pre>
                </article>
              ))}
            </div>
          </section>
        ) : view === "knowledge" ? (
          <KnowledgePanel model={knowledge} />
        ) : (
          <section
            className={`chat-workspace ${useExpandedConversationWidth ? "expanded-content" : ""} ${
              showCollaborationPanel && isCollaborationPanelOpen ? "with-collaboration" : ""
            } ${showCollaborationDag && isCollaborationPanelOpen ? "with-collaboration-dag" : ""}`}
          >
            {showCollaborationDag && isCollaborationPanelOpen ? (
              <CollaborationDag
                activeRole={visibleCollaborationRole}
                agents={agents}
                className="collaboration-dag-standalone"
                onSelectRole={setSelectedCollaborationRole}
                roles={collaborationRoles}
                runStatus={runState?.status ?? ""}
                steps={collaborationSteps}
              />
            ) : null}
            <div className="conversation-column">
              <ModeChooser
                chatMode={chatMode}
                disabled={isStreaming}
                setChatMode={handleChatModeChange}
              />
              <section className="messages" ref={messagesRef}>
                {showCollaborationPanel && !isCollaborationPanelOpen ? (
                  <div className="trace-reveal-row">
                    <button
                      className="collaboration-rail-toggle trace-panel-toggle"
                      onClick={() => setIsCollaborationPanelOpen(true)}
                      type="button"
                    >
                      <PanelRightOpen size={14} />
                      {isAwaitingPlanApproval
                        ? "Review Plan & Continue"
                        : showAutonomousTrace
                          ? "Show Autonomous Trace"
                          : "Show Collaboration Trace"}
                    </button>
                  </div>
                ) : null}
                {messages.length === 0 ? (
                  <div className="empty">
                    <div className="empty-mark"><GitBranch size={22} strokeWidth={1.5} /></div>
                    <span className="empty-eyebrow">Ready to run</span>
                    <h2>What should the agents work on?</h2>
                    <p>
                      Describe an outcome. Choose direct chat for quick work, collaboration for a reviewed plan, or autonomous mode for bounded execution.
                    </p>
                    <div className="starter-prompts" aria-label="Starter prompts">
                      <button onClick={() => setInput("Compare two implementation approaches and recommend one.")} type="button">Compare approaches</button>
                      <button onClick={() => setInput("Research this topic, cite evidence, and summarize the result.")} type="button">Run research</button>
                      <button onClick={() => setInput("Create an execution plan and wait for my approval.")} type="button">Draft a plan</button>
                    </div>
                  </div>
                ) : (
                  <>
                    {messages.map((message) => (
                      <article className={`message ${message.role}`} key={message.id}>
                        <div className="message-meta">{message.role}</div>
                        <div className="bubble">
                          {message.content ? renderMarkdown(message.content) : "..."}
                          <MessageCitations citations={message.citations} />
                        </div>
                      </article>
                    ))}
                  </>
                )}
              </section>
            </div>
            {showCollaborationPanel && isCollaborationPanelOpen ? (
              showAutonomousTrace ? (
                <AutonomousPanel
                  isCanceling={isCancelingRun || runState?.status === "canceling"}
                  humanInputDraft={humanInputDraft}
                  isResuming={isResumingRun}
                  onCancel={handleCancelRun}
                  onCollapse={() => setIsCollaborationPanelOpen(false)}
                  onHumanInputChange={setHumanInputDraft}
                  onResume={handleResumeAutonomous}
                  progress={autonomousProgress}
                  runStatus={runState?.status ?? ""}
                  steps={collaborationSteps}
                />
              ) : (
              <CollaborationPanel
                agents={agents}
                isContinuing={isContinuingRun}
                onContinue={handleContinuePlan}
                onCollapse={() => setIsCollaborationPanelOpen(false)}
                planDraft={planDraft}
                runStatus={runState?.status ?? ""}
                setPlanDraft={setPlanDraft}
                steps={collaborationSteps}
                selectedRole={visibleCollaborationRole}
              />
              )
            ) : null}
          </section>
        )}

        {view === "chat" ? (
          <section className="composer">
            {chatMode === "single" ? (
              <div className="agent-bar single">
                <label className="agent-select">
                  <span>Agent</span>
                  <select
                    title={activeAgent?.name ?? "Select an agent"}
                    value={activeAgentId}
                    disabled={isStreaming || agents.length === 0}
                    onChange={(event) => {
                      const nextAgent = agents.find((agent) => agent.id === event.target.value);
                      setActiveAgentId(event.target.value);
                      setAgentConfigDraft(nextAgent ? agentToConfigDraft(nextAgent) : null);
                      setIsAgentDescriptionExpanded(false);
                      setRunState(null);
                    }}
                  >
                    {agents.map((agent) => (
                      <option key={agent.id} title={agent.name} value={agent.id}>
                        {agent.name}
                      </option>
                    ))}
                  </select>
                </label>
                <div className="agent-summary">
                  <strong>{activeAgent?.name ?? "No agent loaded"}</strong>
                  <div
                    className={`agent-description ${
                      isAgentDescriptionExpanded ? "expanded" : ""
                    }`}
                  >
                    <span>{activeAgent?.description ?? agentsError}</span>
                    {activeAgent?.description ? (
                      <button
                        aria-expanded={isAgentDescriptionExpanded}
                        aria-label={
                          isAgentDescriptionExpanded
                            ? "Collapse agent description"
                            : "Expand agent description"
                        }
                        className="agent-description-toggle"
                        onClick={() =>
                          setIsAgentDescriptionExpanded((current) => !current)
                        }
                      type="button"
                    >
                        {isAgentDescriptionExpanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                      </button>
                    ) : null}
                  </div>
                </div>
                {activeAgent && agentConfigDraft ? (
                  <div className="agent-actions">
                    <button
                      className={`agent-create-button ${isNewAgentFormOpen ? "active" : ""}`}
                      disabled={isCreatingAgent || isStreaming}
                      onClick={handleOpenNewAgentForm}
                      type="button"
                    >
                      <UserRoundPlus size={15} /> New agent
                    </button>
                    <button
                      className={`agent-config-toggle ${isAgentConfigOpen ? "active" : ""}`}
                      onClick={() => {
                        setIsNewAgentFormOpen(false);
                        setNewAgentDraft(null);
                        setAgentConfigStatus("");
                        setIsAgentConfigOpen(true);
                      }}
                      type="button"
                    >
                      <Settings2 size={15} /> Configure
                    </button>
                  </div>
                ) : null}
              </div>
            ) : null}
            <div className="composer-run-options">
              <button
                aria-pressed={completionVerification.enabled}
                className={`verification-config-toggle ${completionVerification.enabled ? "active" : ""}`}
                disabled={isStreaming}
                onClick={handleOpenCompletionVerification}
                type="button"
              >
                <ShieldCheck size={15} />
                <span>Completion verification</span>
                <strong>{completionVerification.enabled ? "On" : "Off"}</strong>
              </button>
            </div>
            {chatMode === "single" && agentsError ? (
              <div className="error">{agentsError}</div>
            ) : null}
            {error ? <div className="error">{error}</div> : null}
            <form className="composer-inner" onSubmit={handleSubmit}>
              <textarea
                disabled={isAwaitingPlanApproval || isAwaitingHumanInput}
                value={input}
                onChange={(event) => setInput(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey) {
                    event.preventDefault();
                    void handleSubmit(event);
                  }
                }}
                placeholder={
                  isAwaitingPlanApproval
                    ? "Review and edit the plan in Collaboration Trace, then continue."
                    : isAwaitingHumanInput
                      ? "Answer the question in Autonomous Trace, then continue."
                    : "Ask AgentFlow anything..."
                }
              />
              <button
                aria-label="Send message"
                title="Send message"
                className="send"
                disabled={isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput || input.trim().length === 0}
              >
                <Send size={18} />
              </button>
            </form>
          </section>
        ) : null}
      </main>
      {completionVerificationDraft ? (
        <div className="modal-backdrop agent-config-modal-backdrop" role="presentation">
          <section
            aria-label="Completion verification"
            aria-modal="true"
            className="agent-config-dialog verification-config-dialog"
            role="dialog"
          >
            <CompletionVerificationPanel
              draft={completionVerificationDraft}
              error={completionVerificationError}
              onCancel={() => {
                setCompletionVerificationDraft(null);
                setCompletionVerificationError("");
              }}
              onChange={(update) => {
                setCompletionVerificationError("");
                setCompletionVerificationDraft((current) => (current ? { ...current, ...update } : current));
              }}
              onSave={handleSaveCompletionVerification}
            />
          </section>
        </div>
      ) : null}
      {chatMode === "single" && isNewAgentFormOpen && newAgentDraft ? (
        <div className="modal-backdrop agent-config-modal-backdrop create-agent-modal-backdrop" role="presentation">
          <section aria-label="Create new agent" aria-modal="true" className="agent-config-dialog" role="dialog">
            <AgentConfigPanel
              actionLabel="Create Agent"
              availableTools={tools}
              draft={newAgentDraft}
              disabled={isStreaming || isCreatingAgent}
              isSaving={isCreatingAgent}
              onCancel={handleCancelNewAgent}
              onChange={updateNewAgentDraft}
              onSave={handleCreateAgent}
              onToggleTool={toggleNewAgentTool}
              status={agentConfigStatus}
              title="Create new agent"
            />
          </section>
        </div>
      ) : null}
      {chatMode === "single" && isAgentConfigOpen && activeAgent && agentConfigDraft ? (
        <div className="modal-backdrop agent-config-modal-backdrop" role="presentation">
          <section aria-label="Edit agent config" aria-modal="true" className="agent-config-dialog" role="dialog">
            <AgentConfigPanel
              actionLabel="Save Config"
              availableTools={tools}
              canArchive={!isDefaultAgent(activeAgent)}
              draft={agentConfigDraft}
              disabled={isStreaming || isSavingAgentConfig}
              isArchiving={archivingAgentId === activeAgent.id}
              isSaving={isSavingAgentConfig}
              onCancel={handleCancelAgentConfig}
              onChange={updateAgentConfigDraft}
              onArchive={handleArchiveAgent}
              onSave={handleSaveAgentConfig}
              onToggleTool={toggleAgentConfigTool}
              status={agentConfigStatus}
              title="Edit agent config"
            />
          </section>
        </div>
      ) : null}
      {agentArchiveCandidate ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-labelledby="archive-agent-title" aria-modal="true" className="confirm-dialog" role="dialog">
            <div>
              <span className="dialog-eyebrow">Archive agent</span>
              <h2 id="archive-agent-title">{agentArchiveCandidate.name}</h2>
              <p>
                This agent will be removed from the active agent list. Existing conversations and replay history will remain available.
              </p>
            </div>
            <div className="confirm-dialog-actions">
              <button className="secondary-action" disabled={Boolean(archivingAgentId)} onClick={cancelArchiveAgent} type="button">
                Cancel
              </button>
              <button className="danger-primary" disabled={Boolean(archivingAgentId)} onClick={confirmArchiveAgent} type="button">
                {archivingAgentId ? "Archiving..." : "Archive Agent"}
              </button>
            </div>
          </section>
        </div>
      ) : null}
      {agentOperationNotice ? (
        <div className="modal-backdrop" role="presentation">
          <section
            aria-labelledby="agent-operation-notice-title"
            aria-modal="true"
            className={`confirm-dialog operation-notice ${agentOperationNotice.tone}`}
            role="dialog"
          >
            <div>
              <span className="dialog-eyebrow">{agentOperationNotice.tone === "success" ? "Success" : "Action failed"}</span>
              <h2 id="agent-operation-notice-title">{agentOperationNotice.title}</h2>
              <p>{agentOperationNotice.message}</p>
            </div>
            <div className="confirm-dialog-actions">
              <button className="send compact-send" onClick={() => setAgentOperationNotice(null)} type="button">
                OK
              </button>
            </div>
          </section>
        </div>
      ) : null}
    </div>
  );
}

function ModeChooser({
  chatMode,
  disabled,
  setChatMode
}: {
  chatMode: ChatMode;
  disabled: boolean;
  setChatMode: (mode: ChatMode) => void;
}) {
  return (
    <section className="mode-chooser" aria-label="Chat mode">
      <button
        className={chatMode === "single" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("single")}
        type="button"
      >
        <Bot size={16} />
        <span>Direct</span>
        <strong>Single agent</strong>
      </button>
      <button
        className={chatMode === "multi_agent" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("multi_agent")}
        type="button"
      >
        <Users size={16} />
        <span>Coordinate</span>
        <strong>Multi-agent</strong>
      </button>
      <button
        className={chatMode === "autonomous" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("autonomous")}
        type="button"
      >
        <Repeat2 size={16} />
        <span>Autonomous</span>
        <strong>Bounded loop</strong>
      </button>
    </section>
  );
}

function AgentConfigPanel({
  actionLabel,
  availableTools,
  canArchive = false,
  disabled,
  draft,
  isArchiving = false,
  isSaving,
  onArchive,
  onCancel,
  onChange,
  onSave,
  onToggleTool,
  status,
  title
}: {
  actionLabel: string;
  availableTools: ToolInfo[];
  canArchive?: boolean;
  disabled: boolean;
  draft: AgentConfigDraft;
  isArchiving?: boolean;
  isSaving: boolean;
  onArchive?: () => void;
  onCancel?: () => void;
  onChange: (update: Partial<AgentConfigDraft>) => void;
  onSave: () => void;
  onToggleTool: (toolName: string) => void;
  status: string;
  title: string;
}) {
  return (
    <section className="agent-config-panel">
      <div className="agent-config-header">
        <strong>{title}</strong>
      </div>
      <div className="agent-config-grid">
        <label>
          <span>Name</span>
          <input disabled={disabled} value={draft.name} onChange={(event) => onChange({ name: event.target.value })} />
        </label>
        <label>
          <span>Executor</span>
          <select
            disabled={disabled}
            value={draft.executor}
            onChange={(event) => onChange({ executor: event.target.value as ChatExecutor })}
          >
            <option value="native">Native</option>
            <option value="langchaingo">LangChainGo</option>
          </select>
        </label>
      </div>
      <label>
        <span>Description</span>
        <textarea
          disabled={disabled}
          value={draft.description}
          onChange={(event) => onChange({ description: event.target.value })}
        />
      </label>
      <label>
        <span>System prompt</span>
        <textarea
          disabled={disabled}
          value={draft.system_prompt}
          onChange={(event) => onChange({ system_prompt: event.target.value })}
        />
      </label>
      <div className="agent-config-switches">
        <label>
          <input
            checked={draft.memory_enabled}
            disabled={disabled}
            onChange={(event) => onChange({ memory_enabled: event.target.checked })}
            type="checkbox"
          />
          <span>Memory retrieval</span>
        </label>
        <label>
          <input
            checked={draft.retrieval_enabled}
            disabled={disabled}
            onChange={(event) => onChange({ retrieval_enabled: event.target.checked })}
            type="checkbox"
          />
          <span>Knowledge retrieval</span>
        </label>
      </div>
      <div className="agent-config-tools">
        <span>Tools</span>
        <div>
          {availableTools.length === 0 ? (
            <small>No tools loaded.</small>
          ) : (
            availableTools.map((tool) => (
              <label key={tool.name}>
                <input
                  checked={draft.tools.includes(tool.name)}
                  disabled={disabled}
                  onChange={() => onToggleTool(tool.name)}
                  type="checkbox"
                />
                <span>{tool.name}</span>
              </label>
            ))
          )}
        </div>
      </div>
      <div className="agent-config-actions">
        {status ? <span className={status.includes("Failed") ? "agent-config-status error-text" : "agent-config-status"}>{status}</span> : null}
        {onArchive && canArchive ? (
          <button className="secondary-action danger-action" disabled={disabled || isArchiving} onClick={onArchive} type="button">
            {isArchiving ? "Archiving..." : "Archive Agent"}
          </button>
        ) : null}
        {onCancel ? (
          <button
            aria-label={`Cancel ${title.toLowerCase()}`}
            className="secondary-action agent-config-cancel"
            disabled={disabled}
            onClick={onCancel}
            type="button"
          >
            <X size={15} /> Cancel
          </button>
        ) : null}
        <button className="send compact-send" disabled={disabled || draft.name.trim().length === 0} onClick={onSave} type="button">
          {isSaving ? "Saving..." : actionLabel}
        </button>
      </div>
    </section>
  );
}

function toCollaborationStepView(step: CollaborationStepView) {
  return {
    role: step.role,
    agent_id: step.agent_id,
    status: step.status,
    iteration: step.iteration,
    input: step.input,
    output: step.output,
    error: step.error
  };
}

function toAutonomousProgress(event: {
  iteration?: number;
  max_iterations?: number;
  elapsed_seconds?: number;
  max_runtime_seconds?: number;
  output_chars?: number;
  max_output_chars?: number;
  tool_calls?: number;
  max_tool_calls?: number;
  stop_reason?: string;
}): AutonomousProgress {
  return {
    iteration: event.iteration ?? 0,
    maxIterations: event.max_iterations ?? 0,
    elapsedSeconds: event.elapsed_seconds ?? 0,
    maxRuntimeSeconds: event.max_runtime_seconds ?? 0,
    outputChars: event.output_chars ?? 0,
    maxOutputChars: event.max_output_chars ?? 0,
    toolCalls: event.tool_calls ?? 0,
    maxToolCalls: event.max_tool_calls ?? 0,
    stopReason: event.stop_reason
  };
}

function upsertCollaborationStep(items: CollaborationStepView[], event: CollaborationStepView) {
  const next = {
    role: event.role,
    agent_id: event.agent_id,
    status: event.status,
    iteration: event.iteration,
    input: event.input,
    output: event.output,
    error: event.error
  };
  const existing = items.findIndex(
    (item) => item.role === event.role && (item.iteration ?? 0) === (event.iteration ?? 0)
  );
  if (existing === -1) {
    return [...items, next];
  }
  return items.map((item, index) => (index === existing ? { ...item, ...next } : item));
}

function AutonomousPanel({
  humanInputDraft,
  isCanceling,
  isResuming,
  onCancel,
  onCollapse,
  onHumanInputChange,
  onResume,
  progress,
  runStatus,
  steps
}: {
  humanInputDraft: string;
  isCanceling: boolean;
  isResuming: boolean;
  onCancel: () => void;
  onCollapse: () => void;
  onHumanInputChange: (value: string) => void;
  onResume: (value?: string) => void;
  progress: AutonomousProgress | null;
  runStatus: string;
  steps: CollaborationStepView[];
}) {
  const activeIterations = groupAutonomousSteps(steps);
  const latestIteration = progress?.iteration ?? activeIterations[activeIterations.length - 1]?.iteration ?? 0;
  const completedSteps = steps.filter((step) => step.status === "completed").length;
  const canStop = runStatus === "running" || runStatus === "canceling" || runStatus === "waiting_for_user";
  const humanInputStep = steps.find((step) => step.role === "human_input" && step.status === "running");

  return (
    <aside className="collaboration-panel autonomous-panel" aria-label="Autonomous trace">
      <div className="collaboration-panel-header">
        <div>
          <span>Autonomous</span>
          <strong>Loop Trace</strong>
        </div>
        <div className="collaboration-panel-actions">
          <small className="trace-progress-label">
            Iteration {latestIteration || 0}
            {progress?.maxIterations ? ` / ${progress.maxIterations}` : ""} · {completedSteps} steps complete
          </small>
          {canStop ? (
            <button
              className="trace-stop"
              disabled={isCanceling || runStatus === "canceling"}
              onClick={onCancel}
              type="button"
            >
              {isCanceling || runStatus === "canceling" ? "Stopping..." : "Stop"}
            </button>
          ) : null}
          <button className="trace-panel-toggle trace-collapse" onClick={onCollapse} type="button">
            <PanelRightClose size={14} /> Hide
          </button>
        </div>
      </div>
      <div className="autonomous-limit-strip" aria-label="Run status">
        <div className="trace-metric">
          <span>Status</span>
          <strong>{runStatus || "idle"}</strong>
        </div>
        <div className="trace-metric">
          <span>Runtime</span>
          <strong>
            {formatDuration(progress?.elapsedSeconds ?? 0)}
            {progress?.maxRuntimeSeconds ? ` / ${formatDuration(progress.maxRuntimeSeconds)}` : ""}
          </strong>
        </div>
        <div className="trace-metric">
          <span>Output</span>
          <strong>
            {progress?.outputChars ?? 0}
            {progress?.maxOutputChars ? ` / ${progress.maxOutputChars}` : ""}
          </strong>
        </div>
        <div className="trace-metric">
          <span>Tool calls</span>
          <strong>
            {progress?.toolCalls ?? 0}
            {progress?.maxToolCalls ? ` / ${progress.maxToolCalls}` : ""}
          </strong>
        </div>
        {progress?.stopReason ? (
          <div className="trace-metric trace-stop-reason">
            <span>Stop reason</span>
            <strong>{progress.stopReason}</strong>
          </div>
        ) : null}
      </div>
      <div className="autonomous-scroll-area">
        {humanInputStep ? (
          <section className="human-input-panel" aria-label="Human input required">
            <div className="human-input-header">
              <div>
                <span>Input required</span>
                <strong>{humanInputStep.output || "Please provide the missing information."}</strong>
              </div>
              <button
                disabled={isResuming || humanInputDraft.trim().length === 0}
                onClick={() => onResume(humanInputDraft)}
                type="button"
              >
                {isResuming ? "Continuing..." : "Submit & Continue"}
              </button>
            </div>
            {humanInputStep.input ? <p>{humanInputStep.input}</p> : null}
            <textarea
              disabled={isResuming}
              onChange={(event) => onHumanInputChange(event.target.value)}
              placeholder="Provide the missing details..."
              value={humanInputDraft}
            />
          </section>
        ) : null}
        <div className="autonomous-iterations">
          {activeIterations.length === 0 ? (
            <div className="autonomous-empty">Waiting for the first autonomous iteration.</div>
          ) : (
            activeIterations.map((group) => (
              <section className="autonomous-iteration" key={group.iteration}>
                <div className="autonomous-iteration-header">
                  <strong>Iteration {group.iteration}</strong>
                  <span>
                    {group.steps.filter((step) => step.status === "completed").length}/{autonomousRoles.length}
                  </span>
                </div>
                {autonomousRoles.map((role) => {
                  const step = group.steps.find((item) => item.role === role.id);
                  return (
                    <article className={`autonomous-step ${step?.status ?? "idle"}`} key={role.id}>
                      <div className="autonomous-step-header">
                        <strong>{role.label}</strong>
                        <span>{step?.status ?? "idle"}</span>
                      </div>
                      <div className="collaboration-output">
                        {step?.output ? renderMarkdown(step.output) : <p>{role.empty}</p>}
                      </div>
                      {step?.error ? <div className="error">{step.error}</div> : null}
                    </article>
                  );
                })}
              </section>
            ))
          )}
        </div>
      </div>
    </aside>
  );
}

function CollaborationPanel({
  agents,
  isContinuing,
  onCollapse,
  onContinue,
  planDraft,
  runStatus,
  selectedRole,
  setPlanDraft,
  steps
}: {
  agents: AgentInfo[];
  isContinuing: boolean;
  onCollapse: () => void;
  onContinue: (plan?: string) => void;
  planDraft: string;
  runStatus: string;
  selectedRole: string;
  setPlanDraft: (value: string) => void;
  steps: CollaborationStepView[];
}) {
  const hasStarted = steps.length > 0;
  const isAwaitingPlanApproval = runStatus === "waiting_for_user";
  const plannerStep = steps.find((step) => step.role === "planner");
  const planEditorRef = useRef<HTMLDivElement | null>(null);
  const agentNames = useMemo(
    () => new Map(agents.map((agent) => [agent.id, agent.name])),
    [agents]
  );
  const visibleSteps = collaborationRoles.map((role, index) => {
    const existing = steps.find((step) => step.role === role.id);
    if (existing) {
      return existing;
    }
    const previousStarted = steps.some(
      (step) => collaborationRoles.findIndex((item) => item.id === step.role) < index
    );
    return { role: role.id, status: hasStarted && previousStarted ? "queued" : "idle" };
  });

  return (
    <aside className="collaboration-panel" aria-label="Multi-agent collaboration">
      <div className="collaboration-panel-header">
        <div>
          <span>Multi-Agent</span>
          <strong>Collaboration Trace</strong>
        </div>
        <div className="collaboration-panel-actions">
          <small className="trace-progress-label">
            {visibleSteps.filter((step) => step.status === "completed").length}/{collaborationRoles.length} complete
          </small>
          <button className="trace-panel-toggle trace-collapse" onClick={onCollapse} type="button">
            <PanelRightClose size={14} /> Hide
          </button>
        </div>
      </div>
      <div className="collaboration-scroll-area">
        {isAwaitingPlanApproval ? (
          <section className="plan-review" aria-label="Review generated plan">
            <div className="plan-review-header">
              <div>
                <span>Action required</span>
                <strong>Review the plan before execution</strong>
              </div>
              <button
                disabled={isContinuing || planDraft.trim().length === 0}
                onClick={() => onContinue(planEditorRef.current?.innerText ?? planDraft)}
                type="button"
              >
                {isContinuing ? "Continuing..." : "Approve & Continue"}
              </button>
            </div>
            <div
              aria-label="Editable generated plan"
              className="plan-rich-editor markdown"
              contentEditable={!isContinuing}
              onBlur={(event) => setPlanDraft(event.currentTarget.innerText)}
              ref={planEditorRef}
              role="textbox"
              suppressContentEditableWarning
              tabIndex={0}
            >
              {renderMarkdownTokens(lexer(planDraft))}
            </div>
            <p>Edit the rendered plan directly. The bottom chat input is paused until you continue this run.</p>
          </section>
        ) : null}
        <div className="collaboration-steps">
          {visibleSteps.map((step) => {
            const role = collaborationRoles.find((item) => item.id === step.role);
            const isPlannerWaiting = step.role === "planner" && isAwaitingPlanApproval;
            return (
              <article
                className={`collaboration-step ${step.status} ${selectedRole === step.role ? "selected" : ""}`}
                key={step.role}
              >
                <div className="collaboration-step-header">
                  <div>
                    <strong>{role?.label ?? step.role}</strong>
                    {step.agent_id ? (
                      <span>
                        {agentNames.get(step.agent_id) ?? "Selected agent"} ({step.agent_id})
                      </span>
                    ) : null}
                  </div>
                  <span className="step-status">{step.status}</span>
                </div>
                <div className="collaboration-output">
                  {isPlannerWaiting ? (
                    plannerStep?.output ? renderMarkdown(plannerStep.output) : <p>Plan is ready for review above.</p>
                  ) : step.output ? (
                    renderMarkdown(step.output)
                  ) : (
                    <p>{role?.empty ?? "Waiting for execution."}</p>
                  )}
                </div>
                {step.error ? <div className="error">{step.error}</div> : null}
              </article>
            );
          })}
        </div>
      </div>
    </aside>
  );
}

const collaborationRoles: CollaborationRole[] = [
  { id: "planner", label: "Planner", empty: "No plan has been generated yet." },
  { id: "router", label: "Router", empty: "Waiting to choose the best worker agent." },
  { id: "worker", label: "Worker", empty: "Waiting for the plan before execution." },
  { id: "reviewer", label: "Reviewer", empty: "Waiting for worker output to review." },
  { id: "finalizer", label: "Finalizer", empty: "Waiting to synthesize the final answer." }
];

const autonomousRoles: CollaborationRole[] = [
  { id: "observe", label: "Observe", empty: "Waiting to observe task state." },
  { id: "plan", label: "Plan", empty: "Waiting to plan the next action." },
  { id: "act", label: "Act", empty: "Waiting to execute the current plan." },
  { id: "review", label: "Review", empty: "Waiting to review the action result." },
  { id: "decide", label: "Decide", empty: "Waiting to decide whether to continue." },
  { id: "human_input", label: "Human Input", empty: "Waiting to see whether user input is needed." },
  { id: "final", label: "Final", empty: "Waiting for final synthesis." }
];

function groupAutonomousSteps(steps: CollaborationStepView[]) {
  const grouped = new Map<number, CollaborationStepView[]>();
  for (const step of steps) {
    const iteration = step.iteration && step.iteration > 0 ? step.iteration : 1;
    grouped.set(iteration, [...(grouped.get(iteration) ?? []), step]);
  }
  return [...grouped.entries()]
    .sort(([left], [right]) => left - right)
    .map(([iteration, items]) => ({ iteration, steps: items }));
}

function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return "0s";
  }
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  if (minutes <= 0) {
    return `${remainingSeconds}s`;
  }
  return `${minutes}m ${remainingSeconds}s`;
}

function formatValue(value: unknown) {
  return JSON.stringify(value, null, 2);
}

function agentToConfigDraft(agent: AgentInfo): AgentConfigDraft {
  return {
    name: agent.name,
    description: agent.description,
    system_prompt: agent.system_prompt,
    tools: agent.tools ?? [],
    memory_enabled: agent.memory_enabled ?? true,
    retrieval_enabled: agent.retrieval_enabled ?? true,
    executor: agent.executor ?? "native"
  };
}

function isDefaultAgent(agent: AgentInfo) {
  return ["agent_research", "agent_coding", "agent_data", "agent_planner"].includes(agent.id);
}

function renderMarkdown(content: string) {
  return <div className="markdown">{renderMarkdownTokens(lexer(content))}</div>;
}

function MessageCitations({ citations }: { citations?: Message["citations"] }) {
  if (!citations || citations.length === 0) {
    return null;
  }
  return (
    <section className="message-citations" aria-label="Source details">
      <div className="message-citations-title">Source details</div>
      <ol>
        {citations.map((citation) => {
          const location = citation.section_path?.filter(Boolean).join(" / ");
          const sourceCount = citation.source_chunk_ids?.length ?? 0;
          return (
            <li key={citation.source_id}>
              <span className="citation-source-id">[{citation.source_id}]</span>
              <span>{citation.document_title || citation.document_id}</span>
              {location ? <span className="citation-location">{location}</span> : null}
              {sourceCount > 1 ? <span className="citation-location">{sourceCount} chunks</span> : null}
            </li>
          );
        })}
      </ol>
    </section>
  );
}

function renderMarkdownTokens(tokens: Token[]) {
  return tokens.map((token, index) => renderMarkdownToken(token, `md-${index}`));
}

function renderMarkdownToken(token: Token, key: string): ReactNode {
  switch (token.type) {
    case "space":
    case "def":
      return null;
    case "heading":
      return renderMarkdownHeading(token.depth, tokenChildren(token), key);
    case "paragraph":
      return <p key={key}>{renderMarkdownTokens(tokenChildren(token))}</p>;
    case "text":
      if ("tokens" in token && token.tokens) {
        return <span key={key}>{renderMarkdownTokens(token.tokens)}</span>;
      }
      return <span key={key}>{token.text}</span>;
    case "strong":
      return <strong key={key}>{renderMarkdownTokens(tokenChildren(token))}</strong>;
    case "em":
      return <em key={key}>{renderMarkdownTokens(tokenChildren(token))}</em>;
    case "del":
      return <del key={key}>{renderMarkdownTokens(tokenChildren(token))}</del>;
    case "codespan":
      return <code key={key}>{token.text}</code>;
    case "br":
      return <br key={key} />;
    case "code":
      return (
        <pre className="markdown-code" key={key}>
          <code data-language={token.lang || undefined}>{token.text}</code>
        </pre>
      );
    case "blockquote":
      return <blockquote key={key}>{renderMarkdownTokens(tokenChildren(token))}</blockquote>;
    case "list":
      return renderMarkdownList(token as Tokens.List, key);
    case "list_item":
      return <li key={key}>{renderMarkdownTokens(tokenChildren(token))}</li>;
    case "link":
      return renderMarkdownLink(token as Tokens.Link, key);
    case "image":
      return renderMarkdownImage(token as Tokens.Image, key);
    case "hr":
      return <hr key={key} />;
    case "table":
      return renderMarkdownTable(token as Tokens.Table, key);
    case "html":
      return null;
    case "escape":
      return <span key={key}>{token.text}</span>;
    default:
      return null;
  }
}

function tokenChildren(token: Token) {
  return "tokens" in token && Array.isArray(token.tokens) ? token.tokens : [];
}

function renderMarkdownHeading(level: number, tokens: Token[], key: string) {
  const content = renderMarkdownTokens(tokens);
  if (level === 1) {
    return <h1 key={key}>{content}</h1>;
  }
  if (level === 2) {
    return <h2 key={key}>{content}</h2>;
  }
  if (level === 3) {
    return <h3 key={key}>{content}</h3>;
  }
  if (level === 4) {
    return <h4 key={key}>{content}</h4>;
  }
  if (level === 5) {
    return <h5 key={key}>{content}</h5>;
  }
  return <h6 key={key}>{content}</h6>;
}

function renderMarkdownList(token: Tokens.List, key: string) {
  const items = token.items.map((item, index) => (
    <li key={`${key}-${index}`}>{renderMarkdownTokens(item.tokens)}</li>
  ));
  if (token.ordered) {
    return (
      <ol key={key} start={typeof token.start === "number" ? token.start : undefined}>
        {items}
      </ol>
    );
  }
  return <ul key={key}>{items}</ul>;
}

function renderMarkdownLink(token: Tokens.Link, key: string) {
  const href = sanitizeMarkdownHref(token.href);
  if (!href) {
    return <span key={key}>{renderMarkdownTokens(token.tokens)}</span>;
  }
  return (
    <a href={href} key={key} rel="noreferrer" target="_blank" title={token.title || undefined}>
      {renderMarkdownTokens(token.tokens)}
    </a>
  );
}

function renderMarkdownImage(token: Tokens.Image, key: string) {
  const href = sanitizeMarkdownHref(token.href);
  if (!href) {
    return <span key={key}>{token.text}</span>;
  }
  // Markdown images may use arbitrary remote or data URLs that Next Image cannot preconfigure.
  // eslint-disable-next-line @next/next/no-img-element
  return <img alt={token.text} key={key} src={href} title={token.title || undefined} />;
}

function renderMarkdownTable(token: Tokens.Table, key: string) {
  return (
    <div className="markdown-table-wrap" key={key}>
      <table>
        <thead>
          <tr>
            {token.header.map((cell, index) => (
              <th key={`${key}-h-${index}`}>{renderMarkdownTokens(cell.tokens)}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {token.rows.map((row, rowIndex) => (
            <tr key={`${key}-r-${rowIndex}`}>
              {row.map((cell, cellIndex) => (
                <td key={`${key}-r-${rowIndex}-${cellIndex}`}>{renderMarkdownTokens(cell.tokens)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function sanitizeMarkdownHref(value: string) {
  const trimmed = value.trim();
  if (/^(https?:|mailto:)/i.test(trimmed)) {
    return trimmed;
  }
  return "";
}
