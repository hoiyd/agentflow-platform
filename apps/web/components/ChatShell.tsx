"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { lexer } from "marked";
import type { Token, Tokens } from "marked";
import {
  AgentInfo,
  Conversation,
  Message,
  ToolInfo,
  createConversation,
  listAgents,
  listConversations,
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
    setView("chat");
    setActiveId(id);
    const loaded = await listMessages(id);
    setMessages(loaded);
  }

  async function startNewConversation() {
    setError("");
    setRunState(null);
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
    if (!content || isStreaming) {
      return;
    }

    setInput("");
    setError("");
    setRunState(null);
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
        <section className="messages">
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
        )}

        {view === "chat" ? (
          <form className="composer" onSubmit={handleSubmit}>
            <div className="agent-bar">
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
              {runState ? (
                <div className={`run-pill ${runState.status}`}>
                  <span>{runState.status}</span>
                  <code>{runState.id}</code>
                </div>
              ) : null}
            </div>
            {agentsError ? <div className="error">{agentsError}</div> : null}
            {error ? <div className="error">{error}</div> : null}
            <div className="composer-inner">
              <textarea
                value={input}
                onChange={(event) => setInput(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" && !event.shiftKey) {
                    event.preventDefault();
                    void handleSubmit(event);
                  }
                }}
                placeholder="Ask AgentFlow anything..."
              />
              <button className="send" disabled={isStreaming || input.trim().length === 0}>
                Send
              </button>
            </div>
          </form>
        ) : null}
      </main>
    </div>
  );
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
