import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { ArrowRight, LoaderCircle } from "lucide-react";
import { getConversationHandoff, type Conversation } from "../../lib/api";

export function ConversationHandoffDialog({ conversation, busy, disabled, error, onClose, onStart }: {
  conversation: Conversation;
  busy: boolean;
  disabled: boolean;
  error: string;
  onClose: () => void;
  onStart: (content: string) => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const title = useId();
  const [preview, setPreview] = useState<Awaited<ReturnType<typeof getConversationHandoff>>>();
  const [draft, setDraft] = useState("");
  const [loadError, setLoadError] = useState("");
  const [submitted, setSubmitted] = useState(false);
  useLayoutEffect(() => {
    const element = dialog.current!;
    element.showModal();
    return () => element.close();
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    void getConversationHandoff(conversation.id, controller.signal).then((result) => {
      if (controller.signal.aborted) return;
      setPreview(result);
      setDraft(result.draft);
    }).catch((err) => {
      if (!controller.signal.aborted) setLoadError(err instanceof Error ? err.message : "Failed to load handoff draft");
    });
    return () => controller.abort();
  }, [conversation.id]);
  const count = Array.from(draft.trim()).length;
  const canStart = !!preview && count > 0 && count <= preview.max_characters && !busy && !disabled;
  return <dialog ref={dialog} className="conversation-handoff-dialog" aria-labelledby={title}
    onCancel={(event) => { event.preventDefault(); if (!busy) onClose(); }}>
    <form onSubmit={(event) => {
      event.preventDefault();
      if (!canStart) return;
      setSubmitted(true);
      onStart(draft.trim());
    }}>
      <header><h2 id={title}>Start with handoff</h2><p>From {conversation.title}</p></header>
      <div className="handoff-body">
        {preview ? <>
          <label><span>First message</span>
            <textarea rows={13} value={draft} disabled={busy} onChange={(event) => { setDraft(event.target.value); setSubmitted(false); }} />
          </label>
          <div className="handoff-meta">
            <span>{preview.truncated ? "Source draft shortened." : ""}</span>
            <span className={count > preview.max_characters ? "over-limit" : ""}>{count} / {preview.max_characters}</span>
          </div>
        </> : !loadError ? <p role="status">Loading handoff draft...</p> : null}
        {loadError || (submitted && error) ? <p className="handoff-error" role="alert">{loadError || error}</p> : null}
      </div>
      <footer>
        <button type="button" className="handoff-secondary" disabled={busy} onClick={onClose}>Cancel</button>
        <button type="submit" className="send" disabled={!canStart}>
          {busy ? <LoaderCircle size={15} className="is-spinning" /> : <ArrowRight size={15} />}
          {busy ? "Starting..." : "Start new conversation"}
        </button>
      </footer>
    </form>
  </dialog>;
}
