"use client";

import { ChangeEvent, FormEvent, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { lexer } from "marked";
import type { Token, Tokens } from "marked";
import {
  AgentInfo,
  ChatExecutor,
  ChatMode,
  Conversation,
  DocumentDetail,
  DocumentInfo,
  EmbeddingInfo,
  Message,
  RAGEvaluationCase,
  RAGEvaluationRunResponse,
  RetrievedDocumentChunk,
  ToolInfo,
  archiveAgent,
  cancelRun,
  continueRun,
  createConversation,
  createAgent,
  createDocument,
  deleteConversation as deleteConversationApi,
  deleteDocument as deleteDocumentApi,
  listCollaborationSteps,
  listAgents,
  listConversations,
  listDocuments,
  listRuns,
  listTools,
  listMessages,
  getDocument,
  resumeRun,
  runRAGEvaluation,
  searchRAG,
  setToolEnabled,
  streamChat,
  updateAgent,
  uploadDocument
} from "../lib/api";
import { CollaborationDag } from "./CollaborationDag";

type DraftMessage = Pick<Message, "role" | "content"> & {
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

const DEFAULT_RAG_EVAL_CASES = `[
  {
    "id": "example_resume_backend",
    "query": "候选人的后端系统设计经验",
    "expected_chunk_contains": ["Go", "PostgreSQL"],
    "min_acceptable_rank": 3,
    "tags": ["resume", "backend"]
  }
]`;

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

export function ChatShell() {
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
  } | null>(null);
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
  const [documents, setDocuments] = useState<DocumentInfo[]>([]);
  const [documentsError, setDocumentsError] = useState("");
  const [documentTitle, setDocumentTitle] = useState("");
  const [documentContent, setDocumentContent] = useState("");
  const [isCreatingDocument, setIsCreatingDocument] = useState(false);
  const [uploadTitle, setUploadTitle] = useState("");
  const [uploadFile, setUploadFile] = useState<File | null>(null);
  const [isUploadingDocument, setIsUploadingDocument] = useState(false);
  const [ragQuery, setRagQuery] = useState("");
  const [ragMinSimilarity, setRagMinSimilarity] = useState("0.15");
  const [ragResults, setRagResults] = useState<RetrievedDocumentChunk[]>([]);
  const [ragEmbedding, setRagEmbedding] = useState<EmbeddingInfo | null>(null);
  const [ragNoMatchReason, setRagNoMatchReason] = useState("");
  const [hasSearchedRAG, setHasSearchedRAG] = useState(false);
  const [isSearchingRAG, setIsSearchingRAG] = useState(false);
  const [ragEvalCases, setRagEvalCases] = useState(DEFAULT_RAG_EVAL_CASES);
  const [ragEvalResult, setRagEvalResult] = useState<RAGEvaluationRunResponse | null>(null);
  const [isRunningRAGEval, setIsRunningRAGEval] = useState(false);
  const [selectedDocument, setSelectedDocument] = useState<DocumentDetail | null>(null);
  const [selectedDocumentId, setSelectedDocumentId] = useState("");
  const [isLoadingDocumentDetail, setIsLoadingDocumentDetail] = useState(false);
  const [deletingDocumentId, setDeletingDocumentId] = useState("");
  const bottomRef = useRef<HTMLDivElement | null>(null);

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId),
    [activeId, conversations]
  );
  const activeAgent = useMemo(
    () => agents.find((agent) => agent.id === activeAgentId),
    [activeAgentId, agents]
  );

  useEffect(() => {
    if (!activeAgent) {
      setAgentConfigDraft(null);
      return;
    }
    setAgentConfigDraft(agentToConfigDraft(activeAgent));
  }, [activeAgent]);
  const showCollaborationPanel = chatMode === "multi_agent" || chatMode === "autonomous";
  const showCollaborationDag = chatMode === "multi_agent";
  const showAutonomousTrace = chatMode === "autonomous";
  const isAwaitingPlanApproval = chatMode === "multi_agent" && runState?.status === "waiting_for_user";
  const isAwaitingHumanInput =
    chatMode === "autonomous" &&
    runState?.status === "waiting_for_user" &&
    collaborationSteps.some((step) => step.role === "human_input" && step.status === "running");
  const isTerminalRun =
    runState?.status === "completed" ||
    runState?.status === "failed" ||
    runState?.status === "canceled";
  const canCancelRun =
    chatMode === "autonomous" &&
    !!runState?.id &&
    !isTerminalRun &&
    (isStreaming ||
      runState.status === "running" ||
      runState.status === "canceling" ||
      runState.status === "waiting_for_user");

  useEffect(() => {
    void refreshConversations();
    void refreshAgents();
    void refreshTools();
    void refreshDocuments();
  }, []);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  useEffect(() => {
    setIsAgentDescriptionExpanded(false);
  }, [activeAgentId]);

  useEffect(() => {
    if (chatMode === "multi_agent" || chatMode === "autonomous") {
      setIsCollaborationPanelOpen(true);
    }
  }, [chatMode]);

  useEffect(() => {
    if (selectedCollaborationRole === "planner") {
      return;
    }
    if (!collaborationSteps.some((step) => step.role === selectedCollaborationRole)) {
      setSelectedCollaborationRole("planner");
    }
  }, [collaborationSteps, selectedCollaborationRole]);

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
        setCollaborationSteps([]);
        setPlanDraft("");
        setHumanInputDraft("");
        return;
      }
      setRunState({
        id: run.id,
        agentId: run.agent_id,
        status: run.status
      });
      const steps = await listCollaborationSteps(run.id);
      setCollaborationSteps(steps.map(toCollaborationStepView));
      if (steps.some((step) => autonomousRoles.some((role) => role.id === step.role))) {
        setChatMode("autonomous");
      } else if (steps.length > 0) {
        setChatMode("multi_agent");
      }
      const planner = steps.find((step) => step.role === "planner");
      setPlanDraft(planner?.output ?? "");
      const humanInput = steps.find((step) => step.role === "human_input" && step.status === "running");
      setHumanInputDraft((current) => (humanInput ? current : ""));
    } catch {
      setCollaborationSteps([]);
      setPlanDraft("");
      setHumanInputDraft("");
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

  async function refreshDocuments() {
    try {
      setDocumentsError("");
      setDocuments(await listDocuments());
      setSelectedDocument(null);
      setSelectedDocumentId("");
    } catch (err) {
      setDocumentsError(err instanceof Error ? err.message : "Failed to load documents");
    }
  }

  async function refreshAgents() {
    try {
      setAgentsError("");
      const items = await listAgents();
      setAgents(items);
      setActiveAgentId((current) => {
        if (current && items.some((agent) => agent.id === current)) {
          return current;
        }
        return items.find((agent) => agent.id === "agent_planner")?.id ?? items[0]?.id ?? "";
      });
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
      setIsAgentConfigOpen(true);
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

  async function handleCreateDocument() {
    const title = documentTitle.trim();
    const content = documentContent.trim();
    if (!title || !content || isCreatingDocument) {
      return;
    }
    setIsCreatingDocument(true);
    setDocumentsError("");
    try {
      const created = await createDocument({
        title,
        content,
        metadata: { source: "ui" }
      });
      setDocuments((items) => [created, ...items]);
      setDocumentTitle("");
      setDocumentContent("");
    } catch (err) {
      setDocumentsError(err instanceof Error ? err.message : "Failed to create document");
    } finally {
      setIsCreatingDocument(false);
    }
  }

  function handleUploadFileChange(event: ChangeEvent<HTMLInputElement>) {
    setUploadFile(event.target.files?.[0] ?? null);
  }

  async function handleUploadDocument() {
    if (!uploadFile || isUploadingDocument) {
      return;
    }
    setIsUploadingDocument(true);
    setDocumentsError("");
    try {
      const created = await uploadDocument({
        file: uploadFile,
        title: uploadTitle
      });
      setDocuments((items) => [created, ...items]);
      setUploadFile(null);
      setUploadTitle("");
    } catch (err) {
      setDocumentsError(err instanceof Error ? err.message : "Failed to upload document");
    } finally {
      setIsUploadingDocument(false);
    }
  }

  async function handleSearchRAG() {
    const query = ragQuery.trim();
    if (!query || isSearchingRAG) {
      return;
    }
    setIsSearchingRAG(true);
    setHasSearchedRAG(true);
    setDocumentsError("");
    try {
      const minSimilarity = Number(ragMinSimilarity);
      const response = await searchRAG({
        query,
        limit: 5,
        min_similarity: Number.isFinite(minSimilarity) ? minSimilarity : 0
      });
      setRagResults(response.items);
      setRagEmbedding(response.embedding ?? null);
      setRagNoMatchReason(response.no_match ? response.reason ?? "No confident match found." : "");
    } catch (err) {
      setDocumentsError(err instanceof Error ? err.message : "Failed to search knowledge");
    } finally {
      setIsSearchingRAG(false);
    }
  }

  async function handleRunRAGEvaluation() {
    if (isRunningRAGEval) {
      return;
    }
    setIsRunningRAGEval(true);
    setDocumentsError("");
    try {
      const parsed = JSON.parse(ragEvalCases) as unknown;
      if (!Array.isArray(parsed)) {
        throw new Error("Evaluation cases must be a JSON array");
      }
      const minSimilarity = Number(ragMinSimilarity);
      const response = await runRAGEvaluation({
        cases: parsed as RAGEvaluationCase[],
        top_k: 5,
        min_similarity: Number.isFinite(minSimilarity) ? minSimilarity : 0
      });
      setRagEvalResult(response);
    } catch (err) {
      setDocumentsError(err instanceof Error ? err.message : "Failed to run retrieval evaluation");
    } finally {
      setIsRunningRAGEval(false);
    }
  }

  async function handleSelectDocument(documentId: string) {
    setSelectedDocumentId(documentId);
    setIsLoadingDocumentDetail(true);
    setDocumentsError("");
    try {
      setSelectedDocument(await getDocument(documentId));
    } catch (err) {
      setDocumentsError(err instanceof Error ? err.message : "Failed to load document");
    } finally {
      setIsLoadingDocumentDetail(false);
    }
  }

  async function handleDeleteDocument(document: DocumentInfo) {
    if (deletingDocumentId) {
      return;
    }
    const confirmed = window.confirm(`Delete knowledge document "${document.title}"? This cannot be undone.`);
    if (!confirmed) {
      return;
    }
    setDeletingDocumentId(document.id);
    setDocumentsError("");
    try {
      await deleteDocumentApi(document.id);
      setDocuments((items) => items.filter((item) => item.id !== document.id));
      setRagResults((items) => items.filter((item) => item.document.id !== document.id));
      if (selectedDocumentId === document.id) {
        setSelectedDocumentId("");
        setSelectedDocument(null);
      }
    } catch (err) {
      setDocumentsError(err instanceof Error ? err.message : "Failed to delete document");
    } finally {
      setDeletingDocumentId("");
    }
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const content = input.trim();
    if (!content || isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput) {
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
          message: content
        },
        (event) => {
          if (event.type === "conversation") {
            conversationId = event.conversation_id;
            setActiveId(event.conversation_id);
          }
          if (event.type === "run") {
            setRunState({
              id: event.run_id,
              agentId: event.agent_id,
              status: event.status
            });
            if (event.status === "canceled" || event.status === "completed" || event.status === "failed") {
              setIsCancelingRun(false);
            }
          }
          if (event.type === "delta") {
            setMessages((items) =>
              items.map((item) =>
                item.id === assistantDraft.id ? { ...item, content: item.content + event.delta } : item
              )
            );
          }
          if (event.type === "collaboration_step") {
            setCollaborationSteps((items) => upsertCollaborationStep(items, event));
            if (event.role === "planner" && event.output) {
              setPlanDraft(event.output);
            }
          }
          if (event.type === "autonomous_progress") {
            setAutonomousProgress(toAutonomousProgress(event));
          }
          if (event.type === "error") {
            setError(event.error);
          }
          if (event.type === "done") {
            setRunState((current) => ({
              id: event.run_id ?? current?.id ?? "",
              agentId: event.agent_id ?? current?.agentId ?? activeAgentId,
              status: event.status ?? "completed"
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
        if (event.type === "run") {
          setRunState({
            id: event.run_id,
            agentId: event.agent_id,
            status: event.status
          });
        }
        if (event.type === "collaboration_step") {
          setCollaborationSteps((items) => upsertCollaborationStep(items, event));
          if (event.role === "planner" && event.output) {
            setPlanDraft(event.output);
          }
        }
        if (event.type === "delta") {
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
          setRunState((current) => ({
            id: event.run_id ?? current?.id ?? runID,
            agentId: event.agent_id ?? current?.agentId ?? activeAgentId,
            status: event.status ?? "completed"
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
        if (event.type === "run") {
          setRunState({
            id: event.run_id,
            agentId: event.agent_id,
            status: event.status
          });
          if (event.status !== "waiting_for_user") {
            setHumanInputDraft("");
          }
        }
        if (event.type === "collaboration_step") {
          setCollaborationSteps((items) => upsertCollaborationStep(items, event));
        }
        if (event.type === "autonomous_progress") {
          setAutonomousProgress(toAutonomousProgress(event));
        }
        if (event.type === "delta") {
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
          setRunState((current) => ({
            id: event.run_id ?? current?.id ?? runID,
            agentId: event.agent_id ?? current?.agentId ?? activeAgentId,
            status: event.status ?? "completed"
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
        status: canceled.status
      });
      if (canceled.status === "canceled" || canceled.status === "completed" || canceled.status === "failed") {
        setIsCancelingRun(false);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to cancel run");
      setIsCancelingRun(false);
    }
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <h1>AgentFlow</h1>
          <p>Agent workflow runtime with runs, memory, tools, and knowledge.</p>
        </div>
        <button className="new-chat" onClick={startNewConversation}>
          New Chat
        </button>
        <div className="sidebar-section">
          <div className="sidebar-section-title">Workspace</div>
          <button
            className={`nav-button ${view === "tools" ? "active" : ""}`}
            onClick={() => {
              setView("tools");
              void refreshTools();
            }}
          >
            Tools
          </button>
          <button
            className={`nav-button ${view === "knowledge" ? "active" : ""}`}
            onClick={() => {
              setView("knowledge");
              void refreshDocuments();
            }}
          >
            Knowledge
          </button>
        </div>
        <div className="sidebar-section-title conversation-section-title">Conversations</div>
        <div className="conversation-list">
          {conversations.map((conversation) => {
            const isActiveConversation = conversation.id === activeId;
            return (
              <div
                className={`conversation-item ${isActiveConversation ? "active" : ""}`}
                key={conversation.id}
                role="button"
                tabIndex={0}
                onClick={() => openConversation(conversation.id)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    event.preventDefault();
                    void openConversation(conversation.id);
                  }
                }}
              >
                <div className="conversation-item-main">
                  <span className="conversation-title">{conversation.title}</span>
                  <span className="conversation-date">
                    {new Date(conversation.updated_at).toLocaleString()}
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
                  Delete
                </button>
              </div>
            );
          })}
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <h2>
            {view === "tools"
              ? "Tools"
              : view === "knowledge"
                ? "Knowledge"
                : activeConversation?.title ?? "New conversation"}
          </h2>
          <div className="topbar-actions">
            {view === "chat" && runState?.id && isTerminalRun ? (
              <a className="run-link" href={`/runs/${runState.id}`}>
                View run
              </a>
            ) : null}
            <span className="status">
              {view === "tools"
                ? `${tools.filter((tool) => tool.enabled).length} enabled`
                : view === "knowledge"
                  ? `${documents.length} documents`
                  : isStreaming
                    ? "Streaming..."
                    : "Ready"}
            </span>
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
                      <div className="tool-source">
                        {tool.source}
                        {tool.source_id ? ` · ${tool.source_id}` : ""}
                      </div>
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
          <KnowledgePanel
            documents={documents}
            documentTitle={documentTitle}
            documentContent={documentContent}
            error={documentsError}
            isCreating={isCreatingDocument}
            isLoadingDocumentDetail={isLoadingDocumentDetail}
            isSearching={isSearchingRAG}
            isRunningEvaluation={isRunningRAGEval}
            isUploading={isUploadingDocument}
            deletingDocumentId={deletingDocumentId}
            minSimilarity={ragMinSimilarity}
            onContentChange={setDocumentContent}
            onCreate={handleCreateDocument}
            onDeleteDocument={handleDeleteDocument}
            onMinSimilarityChange={setRagMinSimilarity}
            onQueryChange={setRagQuery}
            onRefresh={refreshDocuments}
            onRunEvaluation={handleRunRAGEvaluation}
            onSearch={handleSearchRAG}
            onSelectDocument={handleSelectDocument}
            onTitleChange={setDocumentTitle}
            onUpload={handleUploadDocument}
            onUploadFileChange={handleUploadFileChange}
            onUploadTitleChange={setUploadTitle}
            query={ragQuery}
            noMatchReason={ragNoMatchReason}
            evaluationCases={ragEvalCases}
            evaluationResult={ragEvalResult}
            onEvaluationCasesChange={setRagEvalCases}
            hasSearched={hasSearchedRAG}
            searchEmbedding={ragEmbedding}
            results={ragResults}
            selectedDocument={selectedDocument}
            selectedDocumentId={selectedDocumentId}
            uploadFile={uploadFile}
            uploadTitle={uploadTitle}
          />
        ) : (
          <section
            className={`chat-workspace ${
              showCollaborationPanel && isCollaborationPanelOpen ? "with-collaboration" : ""
            }`}
          >
            {showCollaborationDag && isCollaborationPanelOpen ? (
              <CollaborationDag
                activeRole={selectedCollaborationRole}
                agents={agents}
                className="collaboration-dag-standalone"
                onSelectRole={setSelectedCollaborationRole}
                roles={collaborationRoles}
                runStatus={runState?.status ?? ""}
                steps={collaborationSteps}
              />
            ) : null}
            <section className="messages">
              <ModeChooser
                chatMode={chatMode}
                disabled={isStreaming}
                setChatMode={(mode) => {
                  setChatMode(mode);
                  setRunState(null);
                  setIsCancelingRun(false);
                  setCollaborationSteps([]);
                  setAutonomousProgress(null);
                  setHumanInputDraft("");
                  setPlanDraft("");
                }}
              />
              {showCollaborationPanel && !isCollaborationPanelOpen ? (
                <button
                  className="collaboration-rail-toggle"
                  onClick={() => setIsCollaborationPanelOpen(true)}
                  type="button"
                >
                  {isAwaitingPlanApproval
                    ? "Review Plan & Continue"
                    : showAutonomousTrace
                      ? "Show Autonomous Trace"
                      : "Show Collaboration Trace"}
                </button>
              ) : null}
              {messages.length === 0 ? (
                <div className="empty">
                  <h2>Build the first reliable layer.</h2>
                  <p>
                    Start a conversation. The Go API will persist messages and stream assistant output
                    back through Server-Sent Events.
                  </p>
                </div>
              ) : (
                <>
                  {messages.map((message) => (
                    <article className={`message ${message.role}`} key={message.id}>
                      <div className="message-meta">{message.role}</div>
                      <div className="bubble">{message.content ? renderMarkdown(message.content) : "..."}</div>
                    </article>
                  ))}
                </>
              )}
              <div ref={bottomRef} />
            </section>
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
                selectedRole={selectedCollaborationRole}
              />
              )
            ) : null}
          </section>
        )}

        {view === "chat" ? (
          <section className="composer">
            {chatMode === "single" || !runState ? (
              <div className="agent-bar single">
                <label className="agent-select">
                  <span>Agent</span>
                  <select
                    value={activeAgentId}
                    disabled={isStreaming || agents.length === 0}
                    onChange={(event) => {
                      setActiveAgentId(event.target.value);
                      setRunState(null);
                    }}
                  >
                    {agents.map((agent) => (
                      <option key={agent.id} value={agent.id}>
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
                        {isAgentDescriptionExpanded ? "Less" : "..."}
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
                      New Agent
                    </button>
                    <button
                      className={`agent-config-toggle ${isAgentConfigOpen ? "active" : ""}`}
                      onClick={() => {
                        setIsNewAgentFormOpen(false);
                        setNewAgentDraft(null);
                        setAgentConfigStatus("");
                        setIsAgentConfigOpen((current) => !current);
                      }}
                      type="button"
                    >
                      {isAgentConfigOpen ? "Hide Config" : "Agent Config"}
                    </button>
                  </div>
                ) : null}
                {isNewAgentFormOpen && newAgentDraft ? (
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
                ) : null}
                {isAgentConfigOpen && activeAgent && agentConfigDraft ? (
                  <AgentConfigPanel
                    actionLabel="Save Config"
                    availableTools={tools}
                    canArchive={!isDefaultAgent(activeAgent)}
                    draft={agentConfigDraft}
                    disabled={isStreaming || isSavingAgentConfig}
                    isArchiving={archivingAgentId === activeAgent.id}
                    isSaving={isSavingAgentConfig}
                    onChange={updateAgentConfigDraft}
                    onArchive={handleArchiveAgent}
                    onSave={handleSaveAgentConfig}
                    onToggleTool={toggleAgentConfigTool}
                    status={agentConfigStatus}
                    title="Edit agent config"
                  />
                ) : null}
              </div>
            ) : runState ? (
              <div className={`agent-bar ${chatMode}`}>
                <div className={`run-pill ${runState.status}`}>
                  <span>{runState.status}</span>
                  <code>{runState.id}</code>
                </div>
                {chatMode === "autonomous" ? (
                  canCancelRun ? (
                    <button
                      className="run-stop"
                      disabled={isCancelingRun || runState.status === "canceling"}
                      onClick={handleCancelRun}
                      type="button"
                    >
                      {isCancelingRun || runState.status === "canceling" ? "Stopping..." : "Stop"}
                    </button>
                  ) : null
                ) : null}
              </div>
            ) : null}
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
                className="send"
                disabled={isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput || input.trim().length === 0}
              >
                Send
              </button>
            </form>
          </section>
        ) : null}
      </main>
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
        <span>Single Agent</span>
        <strong>Direct chat</strong>
      </button>
      <button
        className={chatMode === "multi_agent" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("multi_agent")}
        type="button"
      >
        <span>Multi-Agent</span>
        <strong>Plan, edit, execute</strong>
      </button>
      <button
        className={chatMode === "autonomous" ? "active" : ""}
        disabled={disabled}
        onClick={() => setChatMode("autonomous")}
        type="button"
      >
        <span>Autonomous</span>
        <strong>Loop until done</strong>
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
        {onCancel ? (
          <button className="secondary-action" disabled={disabled} onClick={onCancel} type="button">
            Cancel
          </button>
        ) : null}
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
        <button className="send compact-send" disabled={disabled || draft.name.trim().length === 0} onClick={onSave} type="button">
          {isSaving ? "Saving..." : actionLabel}
        </button>
      </div>
    </section>
  );
}

function KnowledgePanel({
  documents,
  documentTitle,
  documentContent,
  deletingDocumentId,
  error,
  evaluationCases,
  evaluationResult,
  hasSearched,
  isCreating,
  isLoadingDocumentDetail,
  isRunningEvaluation,
  isSearching,
  isUploading,
  minSimilarity,
  noMatchReason,
  onContentChange,
  onCreate,
  onDeleteDocument,
  onEvaluationCasesChange,
  onMinSimilarityChange,
  onQueryChange,
  onRefresh,
  onRunEvaluation,
  onSearch,
  onSelectDocument,
  onTitleChange,
  onUpload,
  onUploadFileChange,
  onUploadTitleChange,
  query,
  searchEmbedding,
  results,
  selectedDocument,
  selectedDocumentId,
  uploadFile,
  uploadTitle
}: {
  documents: DocumentInfo[];
  documentTitle: string;
  documentContent: string;
  deletingDocumentId: string;
  error: string;
  evaluationCases: string;
  evaluationResult: RAGEvaluationRunResponse | null;
  hasSearched: boolean;
  isCreating: boolean;
  isLoadingDocumentDetail: boolean;
  isRunningEvaluation: boolean;
  isSearching: boolean;
  isUploading: boolean;
  minSimilarity: string;
  noMatchReason: string;
  onContentChange: (value: string) => void;
  onCreate: () => void;
  onDeleteDocument: (document: DocumentInfo) => void;
  onEvaluationCasesChange: (value: string) => void;
  onMinSimilarityChange: (value: string) => void;
  onQueryChange: (value: string) => void;
  onRefresh: () => void;
  onRunEvaluation: () => void;
  onSearch: () => void;
  onSelectDocument: (documentId: string) => void;
  onTitleChange: (value: string) => void;
  onUpload: () => void;
  onUploadFileChange: (event: ChangeEvent<HTMLInputElement>) => void;
  onUploadTitleChange: (value: string) => void;
  query: string;
  searchEmbedding: EmbeddingInfo | null;
  results: RetrievedDocumentChunk[];
  selectedDocument: DocumentDetail | null;
  selectedDocumentId: string;
  uploadFile: File | null;
  uploadTitle: string;
}) {
  return (
    <section className="knowledge-panel">
      {error ? <div className="error">{error}</div> : null}
      <div className="knowledge-grid">
        <section className="knowledge-column">
          <div className="panel-title">Upload knowledge file</div>
          <div className="knowledge-form upload-form">
            <input
              value={uploadTitle}
              onChange={(event) => onUploadTitleChange(event.target.value)}
              placeholder="Optional document title"
            />
            <label className="upload-file-control">
              <input accept=".txt,.md,.markdown,text/plain,text/markdown" onChange={onUploadFileChange} type="file" />
              <span className="upload-file-action">Choose File</span>
              <span className={`upload-file-name ${uploadFile ? "" : "empty"}`}>
                {uploadFile ? `${uploadFile.name} (${formatBytes(uploadFile.size)})` : "No .txt or .md file selected"}
              </span>
            </label>
            <button className="send" disabled={isUploading || !uploadFile} onClick={onUpload} type="button">
              {isUploading ? "Uploading..." : "Upload File"}
            </button>
          </div>
          <div className="panel-title secondary-panel-title">Add text document</div>
          <div className="knowledge-form">
            <input
              value={documentTitle}
              onChange={(event) => onTitleChange(event.target.value)}
              placeholder="Document title"
            />
            <textarea
              value={documentContent}
              onChange={(event) => onContentChange(event.target.value)}
              placeholder="Paste text knowledge here..."
            />
            <button
              className="send"
              disabled={isCreating || documentTitle.trim().length === 0 || documentContent.trim().length === 0}
              onClick={onCreate}
              type="button"
            >
              {isCreating ? "Adding..." : "Add Document"}
            </button>
          </div>
        </section>

        <section className="knowledge-column">
          <div className="knowledge-header-row">
            <div className="panel-title">Documents</div>
            <button className="secondary-action" onClick={onRefresh} type="button">
              Refresh
            </button>
          </div>
          <div className="document-list">
            {documents.length === 0 ? (
              <div className="empty compact">No documents indexed yet.</div>
            ) : (
              documents.map((document) => {
                const isSelected = selectedDocumentId === document.id;
                const detail = selectedDocument?.document.id === document.id ? selectedDocument : null;

                return (
                  <article className="document-card" key={document.id}>
                    <div className="document-card-summary">
                      <div>
                        <h3>{document.title}</h3>
                        <div className="tool-source">
                          {documentFilename(document) || new Date(document.created_at).toLocaleString()}
                        </div>
                      </div>
                      <div className="document-metrics">
                        <span>{documentFormat(document)}</span>
                        <span>{document.chunk_count ?? 0} chunks</span>
                        <span>{document.embedding_count ?? 0} embeddings</span>
                      </div>
                      <div className="document-actions">
                        <button className="secondary-action" onClick={() => onSelectDocument(document.id)} type="button">
                          Details
                        </button>
                        <button
                          className="secondary-action danger-action"
                          disabled={deletingDocumentId === document.id}
                          onClick={() => onDeleteDocument(document)}
                          type="button"
                        >
                          {deletingDocumentId === document.id ? "Deleting..." : "Delete"}
                        </button>
                      </div>
                    </div>
                    {isSelected ? (
                      <DocumentDetailBlock
                        detail={detail}
                        isLoading={isLoadingDocumentDetail && selectedDocumentId === document.id}
                      />
                    ) : null}
                  </article>
                );
              })
            )}
          </div>
        </section>
      </div>

      <section className="knowledge-search">
        <div className="panel-title">Search indexed chunks</div>
        <div className="knowledge-search-row">
          <input
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                onSearch();
              }
            }}
            placeholder="Ask a semantic question..."
          />
          <label className="threshold-input">
            <span>Min similarity</span>
            <input
              max="1"
              min="0"
              onChange={(event) => onMinSimilarityChange(event.target.value)}
              step="0.05"
              type="number"
              value={minSimilarity}
            />
          </label>
          <button className="send" disabled={isSearching || query.trim().length === 0} onClick={onSearch} type="button">
            {isSearching ? "Searching..." : "Search"}
          </button>
        </div>
        <EmbeddingStatus embedding={searchEmbedding} hasSearched={hasSearched} />
        {hasSearched && noMatchReason ? <div className="rag-no-match">{noMatchReason}</div> : null}
        <div className="rag-results">
          {results.length === 0 ? (
            <div className="empty compact">
              {hasSearched && noMatchReason ? "No results passed the relevance gate." : "Search results will appear here."}
            </div>
          ) : (
            results.map((result) => (
              <article className="rag-result-card" key={result.chunk.id}>
                <div className="rag-result-header">
                  <div>
                    <h3>{result.document.title}</h3>
                    <div className="tool-source">{chunkSourceLabel(result)}</div>
                  </div>
                  <div className="document-metrics">
                    <span>{documentFormat(result.document)}</span>
                    {result.confidence ? <span>{result.confidence}</span> : null}
                    <span>similarity {formatScore(result.similarity)}</span>
                    <span>score {formatScore(result.score)}</span>
                  </div>
                </div>
                <ScoreBreakdown result={result} />
                <p>{result.chunk.content}</p>
              </article>
            ))
          )}
        </div>
      </section>

      <section className="knowledge-search rag-evaluation">
        <div className="knowledge-header-row">
          <div className="panel-title">Retrieval evaluation</div>
          <button
            className="send compact-send"
            disabled={isRunningEvaluation || evaluationCases.trim().length === 0}
            onClick={onRunEvaluation}
            type="button"
          >
            {isRunningEvaluation ? "Running..." : "Run Eval"}
          </button>
        </div>
        <textarea
          className="evaluation-cases-input"
          onChange={(event) => onEvaluationCasesChange(event.target.value)}
          spellCheck={false}
          value={evaluationCases}
        />
        <EvaluationResult result={evaluationResult} />
      </section>

    </section>
  );
}

function EvaluationResult({ result }: { result: RAGEvaluationRunResponse | null }) {
  if (!result) {
    return <div className="empty compact">Run evaluation to see hit@k and missed retrieval cases.</div>;
  }
  const total = result.summary.total || 1;
  return (
    <div className="evaluation-result">
      <div className="evaluation-summary">
        <div className="metric">
          <span>Total</span>
          <strong>{result.summary.total}</strong>
        </div>
        <div className="metric">
          <span>Hit@1</span>
          <strong>{formatPercent(result.summary.hit_at_1 / total)}</strong>
        </div>
        <div className="metric">
          <span>Hit@3</span>
          <strong>{formatPercent(result.summary.hit_at_3 / total)}</strong>
        </div>
        <div className="metric">
          <span>Hit@5</span>
          <strong>{formatPercent(result.summary.hit_at_5 / total)}</strong>
        </div>
        <div className={`metric ${result.summary.misses > 0 ? "danger" : ""}`}>
          <span>Misses</span>
          <strong>{result.summary.misses}</strong>
        </div>
      </div>
      <EmbeddingStatus embedding={result.embedding ?? null} hasSearched />
      <div className="evaluation-cases">
        {result.cases.map((item) => (
          <article className={`evaluation-case ${item.hit ? "hit" : "miss"}`} key={item.id}>
            <div className="rag-result-header">
              <div>
                <h3>{item.id}</h3>
                <div className="tool-source">{item.query}</div>
              </div>
              <div className="document-metrics">
                <span>{item.hit ? "hit" : "miss"}</span>
                <span>{item.best_rank ? `rank ${item.best_rank}` : "no match"}</span>
              </div>
            </div>
            <div className="evaluation-expected">{evaluationExpectedLabel(item)}</div>
            {item.failure_reason ? <div className="evaluation-failure">{item.failure_reason}</div> : null}
            <div className="rag-results compact-results">
              {item.items.slice(0, 5).map((resultItem) => (
                <article className="rag-result-card" key={resultItem.chunk.id}>
                  <div className="rag-result-header">
                    <div>
                      <h3>{resultItem.document.title}</h3>
                      <div className="tool-source">{chunkSourceLabel(resultItem)}</div>
                    </div>
                    <div className="document-metrics">
                      <span>v#{resultItem.vector_rank ?? "-"}</span>
                      <span>r#{resultItem.rerank_rank ?? "-"}</span>
                      {resultItem.confidence ? <span>{resultItem.confidence}</span> : null}
                      <span>sim {formatScore(resultItem.similarity)}</span>
                      <span>final {formatScore(resultItem.rerank_score ?? resultItem.score)}</span>
                    </div>
                  </div>
                  <ScoreBreakdown result={resultItem} />
                  <p>{resultItem.chunk.content}</p>
                </article>
              ))}
            </div>
          </article>
        ))}
      </div>
    </div>
  );
}

function ScoreBreakdown({ result }: { result: RetrievedDocumentChunk }) {
  const terms = result.matched_terms ?? [];
  return (
    <div className="score-breakdown">
      <span>base {formatScore(result.score)}</span>
      <span>evidence {formatScore(result.evidence_score ?? 0)}</span>
      <span>coverage {formatPercent(result.evidence_coverage ?? 0)}</span>
      <span>lexical +{formatScore(result.lexical_boost ?? 0)}</span>
      <span>metadata +{formatScore(result.metadata_boost ?? 0)}</span>
      {result.diversity_penalty ? <span>diversity -{formatScore(result.diversity_penalty)}</span> : null}
      {result.confidence ? <span>confidence {result.confidence}</span> : null}
      {terms.length > 0 ? <span>matched {terms.join(", ")}</span> : <span>matched none</span>}
      {result.filter_reason ? <span>{result.filter_reason}</span> : null}
    </div>
  );
}

function DocumentDetailBlock({ detail, isLoading }: { detail: DocumentDetail | null; isLoading: boolean }) {
  if (isLoading) {
    return <div className="document-detail-inline empty compact">Loading document...</div>;
  }

  if (!detail) {
    return <div className="document-detail-inline empty compact">Document detail unavailable.</div>;
  }

  return (
    <div className="document-detail-inline">
      <div className="document-detail-header">
        <div>
          <h3>Document detail</h3>
          <div className="tool-source">
            {[documentFilename(detail.document), new Date(detail.document.created_at).toLocaleString()]
              .filter(Boolean)
              .join(" / ")}
          </div>
        </div>
        <div className="document-metrics">
          <span>{documentFormat(detail.document)}</span>
          <span>{detail.document.chunk_count ?? detail.chunks.length} chunks</span>
          <span>{detail.document.embedding_count ?? 0} embeddings</span>
        </div>
      </div>
      <div className="chunk-list">
        {detail.chunks.map((chunk) => (
          <article className="chunk-card" key={chunk.id}>
            <div className="rag-result-header">
              <div>
                <h3>Chunk {chunk.chunk_index + 1}</h3>
                {metadataString(chunk.metadata, "heading_path") ? (
                  <div className="tool-source">{metadataString(chunk.metadata, "heading_path")}</div>
                ) : null}
              </div>
              <div className="document-metrics">
                {metadataString(chunk.metadata, "chunk_type") ? (
                  <span>{metadataString(chunk.metadata, "chunk_type")}</span>
                ) : null}
                <span>{chunk.token_count} tokens</span>
              </div>
            </div>
            <p>{chunk.content}</p>
          </article>
        ))}
      </div>
    </div>
  );
}

function EmbeddingStatus({ embedding, hasSearched }: { embedding: EmbeddingInfo | null; hasSearched: boolean }) {
  if (!hasSearched) {
    return (
      <div className="embedding-status">
        <span>Embedding: not checked yet</span>
        <span>Run a search to show provider/model.</span>
      </div>
    );
  }

  if (!embedding) {
    return (
      <div className="embedding-status warning">
        <span>Embedding: metadata unavailable</span>
        <span>The API response did not include provider/model; restart the backend if it is still running old code.</span>
      </div>
    );
  }

  return (
    <div className={`embedding-status ${embedding.estimated ? "warning" : ""}`}>
      <span>
        Embedding: {embedding.provider} / {embedding.model}
        {embedding.dimensions ? ` / ${embedding.dimensions}d` : ""}
      </span>
      {embedding.estimated ? (
        <span>Local fallback is active; semantic quality is limited.</span>
      ) : (
        <span>Semantic vector search is using the configured embedding provider.</span>
      )}
    </div>
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
          <small>
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
          <button onClick={onCollapse} type="button">
            Hide
          </button>
        </div>
      </div>
      <div className="autonomous-limit-strip">
        <span>Status: {runStatus || "idle"}</span>
        <span>
          Runtime: {formatDuration(progress?.elapsedSeconds ?? 0)}
          {progress?.maxRuntimeSeconds ? ` / ${formatDuration(progress.maxRuntimeSeconds)}` : ""}
        </span>
        <span>
          Output: {progress?.outputChars ?? 0}
          {progress?.maxOutputChars ? ` / ${progress.maxOutputChars}` : ""}
        </span>
        <span>
          Tool calls: {progress?.toolCalls ?? 0}
          {progress?.maxToolCalls ? ` / ${progress.maxToolCalls}` : ""}
        </span>
        {progress?.stopReason ? <span>Stop: {progress.stopReason}</span> : null}
      </div>
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
          <small>
            {visibleSteps.filter((step) => step.status === "completed").length}/{collaborationRoles.length} complete
          </small>
          <button onClick={onCollapse} type="button">
            Hide
          </button>
        </div>
      </div>
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

function formatScore(value: number) {
  return Number.isFinite(value) ? value.toFixed(3) : "0.000";
}

function formatPercent(value: number) {
  return Number.isFinite(value) ? `${Math.round(value * 100)}%` : "0%";
}

function evaluationExpectedLabel(item: RAGEvaluationRunResponse["cases"][number]) {
  const parts = [
    item.expected_document_ids?.length ? `documents: ${item.expected_document_ids.join(", ")}` : "",
    item.expected_chunk_ids?.length ? `chunks: ${item.expected_chunk_ids.join(", ")}` : "",
    item.expected_chunk_contains?.length ? `contains: ${item.expected_chunk_contains.join(", ")}` : ""
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" / ") : "No expectations configured";
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

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return "0 B";
  }
  if (value < 1024) {
    return `${value} B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(1)} KB`;
  }
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

function metadataString(metadata: Record<string, unknown> | undefined, key: string) {
  const value = metadata?.[key];
  return typeof value === "string" ? value : "";
}

function documentFormat(document: DocumentInfo) {
  return metadataString(document.metadata, "format") || document.source_type || "text";
}

function documentFilename(document: DocumentInfo) {
  return metadataString(document.metadata, "filename") || document.source_uri || "";
}

function chunkSourceLabel(result: RetrievedDocumentChunk) {
  const parts = [
    documentFilename(result.document),
    metadataString(result.chunk.metadata, "heading_path"),
    metadataString(result.chunk.metadata, "chunk_type")
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" / ") : `Chunk ${result.chunk.chunk_index + 1}`;
}

function renderMarkdown(content: string) {
  return <div className="markdown">{renderMarkdownTokens(lexer(content))}</div>;
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
