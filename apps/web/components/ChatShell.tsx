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
  continueRun,
  createConversation,
  listCollaborationSteps,
  listAgents,
  listConversations,
  listRuns,
  listTools,
  listMessages,
  setToolEnabled,
  streamChat
} from "../lib/api";

type DraftMessage = Pick<Message, "role" | "content"> & {
  id: string;
  conversation_id: string;
  created_at: string;
};

type CollaborationStepView = {
  role: string;
  agent_id?: string;
  status: string;
  input?: string;
  output?: string;
  error?: string;
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
  const [chatMode, setChatMode] = useState<ChatMode>("single");
  const [collaborationSteps, setCollaborationSteps] = useState<CollaborationStepView[]>([]);
  const [isCollaborationPanelOpen, setIsCollaborationPanelOpen] = useState(true);
  const [planDraft, setPlanDraft] = useState("");
  const [isContinuingRun, setIsContinuingRun] = useState(false);
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
  const showCollaborationPanel = chatMode === "multi_agent";
  const isAwaitingPlanApproval = chatMode === "multi_agent" && runState?.status === "waiting_for_user";

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
    if (chatMode === "multi_agent") {
      setIsCollaborationPanelOpen(true);
    }
  }, [chatMode]);

  async function refreshConversations(nextActiveId?: string) {
    const items = await listConversations();
    setConversations(items);
    if (nextActiveId) {
      setActiveId(nextActiveId);
      return;
    }
    if (!activeId && items[0]) {
      setActiveId(items[0].id);
      const loaded = await listMessages(items[0].id);
      setMessages(loaded);
      await refreshCollaborationSteps(items[0].id);
    }
  }

  async function refreshCollaborationSteps(conversationId: string) {
    try {
      const runs = await listRuns();
      const run = runs.find((item) => item.conversation_id === conversationId);
      if (!run) {
        setCollaborationSteps([]);
        setPlanDraft("");
        return;
      }
      setRunState({
        id: run.id,
        agentId: run.agent_id,
        status: run.status
      });
      const steps = await listCollaborationSteps(run.id);
      setCollaborationSteps(steps.map(toCollaborationStepView));
      const planner = steps.find((step) => step.role === "planner");
      setPlanDraft(planner?.output ?? "");
    } catch {
      setCollaborationSteps([]);
      setPlanDraft("");
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
    setPlanDraft("");
    setView("chat");
    setActiveId(id);
    const loaded = await listMessages(id);
    setMessages(loaded);
    await refreshCollaborationSteps(id);
  }

  async function startNewConversation() {
    setError("");
    setRunState(null);
    setCollaborationSteps([]);
    setPlanDraft("");
    setView("chat");
    const conversation = await createConversation("New conversation");
    setConversations((items) => [conversation, ...items]);
    setActiveId(conversation.id);
    setMessages([]);
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
    if (!content || isStreaming || isAwaitingPlanApproval) {
      return;
    }

    setInput("");
    setError("");
    setRunState(null);
    setCollaborationSteps([]);
    setPlanDraft("");
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
          if (event.type === "error") {
            setError(event.error);
          }
          if (event.type === "done") {
            setRunState((current) => ({
              id: event.run_id ?? current?.id ?? "",
              agentId: event.agent_id ?? current?.agentId ?? activeAgentId,
              status: event.status ?? "completed"
            }));
          }
        }
      );

      await refreshConversations(conversationId);
      if (conversationId) {
        const persisted = await listMessages(conversationId);
        setMessages(persisted);
      }
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
          {conversations.map((conversation) => (
            <button
              className={`conversation-item ${conversation.id === activeId ? "active" : ""}`}
              key={conversation.id}
              onClick={() => openConversation(conversation.id)}
            >
              <span className="conversation-title">{conversation.title}</span>
              <span className="conversation-date">
                {new Date(conversation.updated_at).toLocaleString()}
              </span>
            </button>
          ))}
        </div>
      </aside>

      <main className="main">
        <header className="topbar">
          <h2>{view === "tools" ? "Tools" : activeConversation?.title ?? "New conversation"}</h2>
          <span className="status">
            {view === "tools"
              ? `${tools.filter((tool) => tool.enabled).length} enabled`
              : isStreaming
                ? "Streaming..."
                : "Ready"}
          </span>
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
            <section className="messages">
              <ModeChooser
                chatMode={chatMode}
                disabled={isStreaming}
                setChatMode={(mode) => {
                  setChatMode(mode);
                  setRunState(null);
                  if (mode === "single") {
                    setCollaborationSteps([]);
                    setPlanDraft("");
                  }
                }}
              />
              {showCollaborationPanel && !isCollaborationPanelOpen ? (
                <button
                  className="collaboration-rail-toggle"
                  onClick={() => setIsCollaborationPanelOpen(true)}
                  type="button"
                >
                  {isAwaitingPlanApproval ? "Review Plan & Continue" : "Show Collaboration Trace"}
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
              <CollaborationPanel
                isContinuing={isContinuingRun}
                onContinue={handleContinuePlan}
                onCollapse={() => setIsCollaborationPanelOpen(false)}
                planDraft={planDraft}
                runStatus={runState?.status ?? ""}
                setPlanDraft={setPlanDraft}
                steps={collaborationSteps}
              />
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
              <div className="agent-bar multi_agent">
                <div className={`run-pill ${runState.status}`}>
                  <span>{runState.status}</span>
                  <code>{runState.id}</code>
                </div>
              </div>
            ) : null}
            {chatMode === "single" && agentsError ? (
              <div className="error">{agentsError}</div>
            ) : null}
            {error ? <div className="error">{error}</div> : null}
            <div className="composer-inner">
              <textarea
                disabled={isAwaitingPlanApproval}
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
                    : "Ask AgentFlow anything..."
                }
              />
              <button
                className="send"
                disabled={isStreaming || isAwaitingPlanApproval || input.trim().length === 0}
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
    </section>
  );
}

function toCollaborationStepView(step: CollaborationStepView) {
  return {
    role: step.role,
    agent_id: step.agent_id,
    status: step.status,
    input: step.input,
    output: step.output,
    error: step.error
  };
}

function upsertCollaborationStep(items: CollaborationStepView[], event: CollaborationStepView) {
  const next = {
    role: event.role,
    agent_id: event.agent_id,
    status: event.status,
    input: event.input,
    output: event.output,
    error: event.error
  };
  const existing = items.findIndex((item) => item.role === event.role);
  if (existing === -1) {
    return [...items, next];
  }
  return items.map((item, index) => (index === existing ? { ...item, ...next } : item));
}

function CollaborationPanel({
  isContinuing,
  onCollapse,
  onContinue,
  planDraft,
  runStatus,
  setPlanDraft,
  steps
}: {
  isContinuing: boolean;
  onCollapse: () => void;
  onContinue: (plan?: string) => void;
  planDraft: string;
  runStatus: string;
  setPlanDraft: (value: string) => void;
  steps: CollaborationStepView[];
}) {
  const hasStarted = steps.length > 0;
  const isAwaitingPlanApproval = runStatus === "waiting_for_user";
  const plannerStep = steps.find((step) => step.role === "planner");
  const planEditorRef = useRef<HTMLDivElement | null>(null);
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
          <small>{visibleSteps.filter((step) => step.status === "completed").length}/4 complete</small>
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
            <article className={`collaboration-step ${step.status}`} key={step.role}>
              <div className="collaboration-step-header">
                <div>
                  <strong>{role?.label ?? step.role}</strong>
                  {step.agent_id ? <span>{step.agent_id}</span> : null}
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

const collaborationRoles = [
  { id: "planner", label: "Planner", empty: "No plan has been generated yet." },
  { id: "worker", label: "Worker", empty: "Waiting for the plan before execution." },
  { id: "reviewer", label: "Reviewer", empty: "Waiting for worker output to review." },
  { id: "finalizer", label: "Finalizer", empty: "Waiting to synthesize the final answer." }
];

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
