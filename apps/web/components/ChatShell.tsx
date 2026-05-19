"use client";

import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import {
  Conversation,
  Message,
  createConversation,
  listConversations,
  listMessages,
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
  const bottomRef = useRef<HTMLDivElement | null>(null);

  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId),
    [activeId, conversations]
  );

  useEffect(() => {
    void refreshConversations();
  }, []);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

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

  async function openConversation(id: string) {
    setError("");
    setActiveId(id);
    const loaded = await listMessages(id);
    setMessages(loaded);
  }

  async function startNewConversation() {
    setError("");
    const conversation = await createConversation("New conversation");
    setConversations((items) => [conversation, ...items]);
    setActiveId(conversation.id);
    setMessages([]);
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const content = input.trim();
    if (!content || isStreaming) {
      return;
    }

    setInput("");
    setError("");
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
          <h2>{activeConversation?.title ?? "New conversation"}</h2>
          <span className="status">{isStreaming ? "Streaming..." : "Ready"}</span>
        </header>

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
            messages.map((message) => (
              <article className={`message ${message.role}`} key={message.id}>
                <div className="message-meta">{message.role}</div>
                <div className="bubble">{message.content || "..."}</div>
              </article>
            ))
          )}
          <div ref={bottomRef} />
        </section>

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
      </main>
    </div>
  );
}
