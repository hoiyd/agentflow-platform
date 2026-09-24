import { useEffect, useMemo, useState } from "react";
import { updateConversationTitle, type Conversation } from "../../lib/api";
import { createLatestRequestController } from "../../lib/latest-request";
import type { DraftMessage } from "./runEventProjection";

export function useConversationWorkspace() {
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [activeId, setActiveId] = useState("");
  const [messages, setMessages] = useState<DraftMessage[]>([]);
  const [input, setInput] = useState("");
  const [error, setError] = useState("");
  const [editingTitle, setEditingTitle] = useState<{ id: string; draft: string } | null>(null);
  const [isSavingConversationTitle, setIsSavingConversationTitle] = useState(false);
  const [titleRequests] = useState(createLatestRequestController);
  useEffect(() => () => titleRequests.cancel(), [titleRequests]);
  const activeConversation = useMemo(
    () => conversations.find((conversation) => conversation.id === activeId),
    [activeId, conversations]
  );

  function startEditingTitle(conversation: Conversation) {
    titleRequests.cancel();
    setIsSavingConversationTitle(false);
    setEditingTitle({ id: conversation.id, draft: conversation.title });
    setError("");
  }

  function cancelEditingTitle() {
    titleRequests.cancel();
    setIsSavingConversationTitle(false);
    setEditingTitle(null);
  }

  function setConversationTitleDraft(draft: string) {
    setEditingTitle((current) => current ? { ...current, draft } : current);
  }

  async function saveTitle() {
    const conversationId = editingTitle?.id;
    const title = editingTitle?.draft.trim();
    if (!conversationId || !title || isSavingConversationTitle) return;
    const request = titleRequests.begin();
    setIsSavingConversationTitle(true);
    setError("");
    try {
      const updated = await updateConversationTitle(conversationId, title);
      if (!request.isCurrent()) return;
      setConversations((items) => items.map((item) => item.id === updated.id ? updated : item));
      setEditingTitle(null);
    } catch (err) {
      if (request.isCurrent()) setError(err instanceof Error ? err.message : "Failed to update conversation title");
    } finally {
      if (request.isCurrent()) setIsSavingConversationTitle(false);
    }
  }

  function applyTitle(conversationId: string, title?: string) {
    const trimmed = title?.trim();
    if (!trimmed) return;
    setConversations((items) => items.map((item) => item.id === conversationId
      ? { ...item, title: trimmed, updated_at: new Date().toISOString() }
      : item
    ));
  }

  return {
    conversations, setConversations, activeId, setActiveId, activeConversation,
    messages, setMessages, input, setInput, error, setError,
    editingConversationId: editingTitle?.id ?? "", conversationTitleDraft: editingTitle?.draft ?? "",
    isSavingConversationTitle, startEditingTitle, cancelEditingTitle, setConversationTitleDraft,
    saveTitle, applyTitle
  };
}
