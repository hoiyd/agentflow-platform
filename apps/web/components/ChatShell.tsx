"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import {
  Conversation,
  Message,
  ToolInfo,
  createConversation,
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

type ToolTrace = {
  id: string;
  name: string;
  status: "running" | "completed" | "failed";
  arguments: string;
  result?: string;
  durationMs?: number;
  error?: string;
};

export function ChatShell() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeId, setActiveId] = useState<string>("");
  const [messages, setMessages] = useState<DraftMessage[]>([]);
  const [toolTraces, setToolTraces] = useState<ToolTrace[]>([]);
  const [input, setInput] = useState("");
  const [isStreaming, setIsStreaming] = useState(false);
  const [error, setError] = useState("");
  const [view, setView] = useState<"chat" | "tools">("chat");
  const [tools, setTools] = useState<ToolInfo[]>([]);
  const [toolsError, setToolsError] = useState("");
  const [updatingTool, setUpdatingTool] = useState("");
  const bottomRef = useRef<HTMLDivElement | null>(null);

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId),
    [activeId, conversations]
  );

  useEffect(() => {
    void refreshConversations();
    void refreshTools();
  }, []);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, toolTraces]);

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

  async function openConversation(id: string) {
    setError("");
    setView("chat");
    setActiveId(id);
    const loaded = await listMessages(id);
    setMessages(loaded);
    setToolTraces([]);
  }

  async function startNewConversation() {
    setError("");
    setView("chat");
    const conversation = await createConversation("New conversation");
    setConversations((items) => [conversation, ...items]);
    setActiveId(conversation.id);
    setMessages([]);
    setToolTraces([]);
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
    setToolTraces([]);
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
      await streamChat({ conversation_id: conversationId || undefined, message: content }, (event) => {
        if (event.type === "conversation") {
          conversationId = event.conversation_id;
          setActiveId(event.conversation_id);
        }
        if (event.type === "delta") {
          setMessages((items) =>
            items.map((item) =>
              item.id === assistantDraft.id ? { ...item, content: item.content + event.delta } : item
            )
          );
        }
        if (event.type === "tool_start") {
          setToolTraces((items) => [
            ...items,
            {
              id: event.tool_call_id,
              name: event.tool_name,
              status: "running",
              arguments: event.arguments ?? ""
            }
          ]);
        }
        if (event.type === "tool_end" || event.type === "tool_error") {
          setToolTraces((items) =>
            items.map((item) =>
              item.id === event.tool_call_id
                ? {
                    ...item,
                    status: event.type === "tool_error" ? "failed" : "completed",
                    arguments: event.arguments ?? item.arguments,
                    result: event.result,
                    durationMs: event.duration_ms,
                    error: event.error
                  }
                : item
            )
          );
        }
        if (event.type === "error") {
          setError(event.error);
        }
      });

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
          <p>Day 1 chat runtime with Go streaming backend.</p>
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
                  <div className="bubble">{message.content || "..."}</div>
                </article>
              ))}
              {toolTraces.length > 0 ? (
                <section className="tool-traces" aria-label="Tool calls">
                  <div className="tool-traces-title">Tool calls</div>
                  {toolTraces.map((trace) => (
                    <article className={`tool-trace ${trace.status}`} key={trace.id}>
                      <div className="tool-trace-header">
                        <span>{trace.name}</span>
                        <span>{trace.status}{trace.durationMs !== undefined ? ` · ${trace.durationMs}ms` : ""}</span>
                      </div>
                      <div className="tool-trace-grid">
                        <div>
                          <div className="tool-label">Arguments</div>
                          <pre>{formatJSON(trace.arguments)}</pre>
                        </div>
                        <div>
                          <div className="tool-label">{trace.status === "failed" ? "Error" : "Result"}</div>
                          <pre>{trace.error || formatJSON(trace.result ?? "") || "Waiting..."}</pre>
                        </div>
                      </div>
                    </article>
                  ))}
                </section>
              ) : null}
            </>
          )}
          <div ref={bottomRef} />
        </section>
        )}

        {view === "chat" ? (
        <form className="composer" onSubmit={handleSubmit}>
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

function formatJSON(value: string) {
  if (!value) {
    return "";
  }
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}

function formatValue(value: unknown) {
  return JSON.stringify(value, null, 2);
}
