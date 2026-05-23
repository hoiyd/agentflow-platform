"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { lexer } from "marked";
import type { Token, Tokens } from "marked";
import {
  AgentInfo,
  ChatMode,
  Conversation,
  Message,
  ToolInfo,
  cancelRun,
  continueRun,
  createConversation,
  deleteConversation as deleteConversationApi,
  listCollaborationSteps,
  listAgents,
  listConversations,
  listRuns,
  listTools,
  listMessages,
  resumeRun,
  setToolEnabled,
  streamChat
} from "../lib/api";
import { CollaborationDag } from "./CollaborationDag";

type DraftMessage = Pick<Message, "role" | "content"> & {
  id: string;
  conversation_id: string;
  created_at: string;
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
  const [view, setView] = useState<"chat" | "tools">("chat");
  const [tools, setTools] = useState<ToolInfo[]>([]);
  const [toolsError, setToolsError] = useState("");
  const [updatingTool, setUpdatingTool] = useState("");
  const bottomRef = useRef<HTMLDivElement | null>(null);

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
          agent_id: chatMode === "single" ? activeAgentId || undefined : undefined,
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
          <p>Day 3 agent runtime with runs, memory, and tools.</p>
        </div>
        <button className="new-chat" onClick={startNewConversation}>
          New Chat
        </button>
        <button
          className={`nav-button ${view === "tools" ? "active" : ""}`}
          onClick={() => {
            setView("tools");
            void refreshTools();
          }}
        >
          Tools
        </button>
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
          <h2>{view === "tools" ? "Tools" : activeConversation?.title ?? "New conversation"}</h2>
          <div className="topbar-actions">
            {view === "chat" && runState?.id && isTerminalRun ? (
              <a className="run-link" href={`/runs/${runState.id}`}>
                View run
              </a>
            ) : null}
            <span className="status">
              {view === "tools"
                ? `${tools.filter((tool) => tool.enabled).length} enabled`
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
          <form className="composer" onSubmit={handleSubmit}>
            {chatMode === "single" ? (
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
            <div className="composer-inner">
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
            </div>
          </form>
        ) : null}
      </main>
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
