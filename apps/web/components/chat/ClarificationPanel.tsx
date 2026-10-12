import { useState } from "react";
import { MessageCircleQuestion, Play, Send } from "lucide-react";
import type { Clarification } from "../../lib/clarification-api";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";

type Props = {
  items: Clarification[];
  recoverable?: boolean;
  busy?: boolean;
  onResume?: (question: Clarification, answers: Record<string, string>) => void;
  conversationHref?: string;
};

export function ClarificationPanel({ items, busy = false, onResume, conversationHref, recoverable = false }: Props) {
  const readOnly = useWorkspaceReadOnly();
  const latest = items.at(-1);
  const pending = items.find(q => q.status === "pending" || q.status === "answered")
    ?? (recoverable && latest?.status === "applied" ? latest : undefined);
  const history = items.filter(q => q.id !== pending?.id);
  if (!items.length) return null;

  return <section className="clarification-panel" aria-label="Run clarification">
    {pending ? <QuestionForm key={pending.id} question={pending} busy={busy}
      onResume={readOnly ? undefined : onResume} conversationHref={conversationHref} /> : null}
    {history.length ? <details className="clarification-history">
      <summary>Clarifications ({history.length})</summary>
      {history.map(q => <article key={q.id}>
        <div className="clarification-receipt">
          <strong>{q.status}</strong>
          <span>{q.stage_id ? `Stage ${q.stage_id}` : `Turn ${q.turn_id}`}</span>
          {q.tool_call_id ? <span>Call {q.tool_call_id}</span> : null}
          {q.applied_at ? <time dateTime={q.applied_at}>{new Date(q.applied_at).toLocaleString()}</time> : null}
        </div>
        <dl>{q.questions.map(item => <div key={item.id}>
          <dt>{item.question}</dt><dd>{q.answers?.[item.id] ?? "No answer received"}</dd>
        </div>)}</dl>
      </article>)}
    </details> : null}
  </section>;
}

function QuestionForm({ question, busy, onResume, conversationHref }: {
  question: Clarification;
  busy: boolean;
  onResume?: Props["onResume"];
  conversationHref?: string;
}) {
  const [answers, setAnswers] = useState<Record<string, string>>({});
  const saved = question.status === "answered" || question.status === "applied";
  const ready = question.questions.every(q => answers[q.id]?.trim());

  return <form onSubmit={event => {
    event.preventDefault();
    if (!busy && (saved || ready)) onResume?.(question, saved ? question.answers ?? {} : answers);
  }}>
    <header><MessageCircleQuestion size={16} aria-hidden="true" />
      <strong>{saved ? "Answer saved" : "Clarification needed"}</strong>
    </header>
    {question.questions.map((q, index) => <fieldset key={q.id} disabled={busy || !onResume}>
      <legend>{q.question}</legend>
      {saved ? <p>{question.answers?.[q.id]}</p> : <>
        {q.options?.length ? <div className="clarification-options">
          {q.options.map(option => <label key={option}>
            <input type="radio" name={`${question.id}-${q.id}`} checked={answers[q.id] === option}
              onChange={() => setAnswers(a => ({ ...a, [q.id]: option }))} />
            {option}
          </label>)}
        </div> : null}
        {onResume ? <div className="clarification-answer">
          <label htmlFor={`${question.id}-${q.id}-answer`}>
            {question.questions.length > 1 ? `Your answer ${index + 1}` : "Your answer"}
          </label>
          <textarea id={`${question.id}-${q.id}-answer`} rows={2} maxLength={8192}
            value={answers[q.id] ?? ""}
            onChange={event => setAnswers(a => ({ ...a, [q.id]: event.target.value }))} />
        </div> : null}
      </>}
    </fieldset>)}
    <div className="clarification-actions">
      {onResume ? <button type="submit" disabled={busy || (!saved && !ready)}>
        {saved ? <Play size={14} /> : <Send size={14} />}
        {busy ? "Continuing..." : saved ? "Continue run" : "Answer and continue"}
      </button> : conversationHref ? <a href={conversationHref}>Open conversation to answer</a> : null}
    </div>
  </form>;
}
