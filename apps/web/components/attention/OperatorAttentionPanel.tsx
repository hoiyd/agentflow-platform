"use client";

import Link from "next/link";
import { ArrowUpRight, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";

import { listRunAttention, type OperatorAttentionItem } from "../../lib/api";

export function OperatorAttentionPanel() {
  const [items, setItems] = useState<OperatorAttentionItem[] | null>(null);
  const [error, setError] = useState("");
  const [isRefreshing, setIsRefreshing] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void listRunAttention(controller.signal)
      .then(setItems)
      .catch((err) => {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : "Failed to load operator attention");
      });
    return () => controller.abort();
  }, []);

  async function refresh() {
    setIsRefreshing(true);
    setError("");
    try {
      setItems(await listRunAttention());
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to load operator attention");
    } finally {
      setIsRefreshing(false);
    }
  }

  return (
    <section className="attention-panel">
      <header className="attention-toolbar">
        <div>
          <span>Operator queue</span>
          <strong>{items?.length ?? "-"} open</strong>
        </div>
        <button disabled={isRefreshing} onClick={() => void refresh()} type="button">
          <RefreshCw aria-hidden="true" size={14} /> {isRefreshing ? "Refreshing" : "Refresh"}
        </button>
      </header>

      {error ? <div className="attention-error" role="alert">{error}</div> : null}
      {items === null && !error ? <div className="attention-empty">Loading attention queue...</div> : null}
      {items?.length === 0 ? <div className="attention-empty">No runs need operator attention.</div> : null}
      {items && items.length > 0 ? (
        <div className="attention-list">
          {items.map((item) => <AttentionRow item={item} key={item.run_id} />)}
        </div>
      ) : null}
    </section>
  );
}

function AttentionRow({ item }: { item: OperatorAttentionItem }) {
  const evidence = item.evidence[0];
  const action = item.recommended_action;
  const actionText = action
    ? `${action.label}${action.enabled ? "" : ` unavailable${action.unavailable_reason ? `: ${action.unavailable_reason}` : ""}`}`
    : "No automated recovery action";
  return (
    <article className="attention-row">
      <div className="attention-rank"><span>{reasonLabel(item.reason)}</span><code>{item.run_status}</code></div>
      <div className="attention-main">
        <div className="attention-heading">
          <div><h2>{item.title}</h2><span>{item.conversation_title || item.conversation_id}</span></div>
          <time dateTime={item.updated_at}>{new Date(item.updated_at).toLocaleString()}</time>
        </div>
        <p>{item.message}</p>
        {evidence ? <div className="attention-evidence"><strong>{evidence.kind.replaceAll("_", " ")}</strong><span>{evidence.summary}</span>{item.evidence.length > 1 ? <code>+{item.evidence.length - 1}</code> : null}</div> : <div className="attention-evidence empty">No detailed evidence recorded</div>}
        <div className="attention-footer">
          <div>
            <span>Observed at event {item.observation_sequence}</span>
            <span className={action && !action.enabled ? "disabled" : ""}>{actionText}</span>
          </div>
          <Link href={`/runs/${encodeURIComponent(item.run_id)}#recovery-summary`}>Review run <ArrowUpRight aria-hidden="true" size={14} /></Link>
        </div>
      </div>
    </article>
  );
}

function reasonLabel(reason: OperatorAttentionItem["reason"]) {
  return ({
    reconciliation_required: "Reconcile",
    recovery_available: "Recover",
    waiting_for_user: "Input",
    verification_attention: "Verify",
    budget_exhausted: "Budget",
    failure: "Failure"
  } as const)[reason];
}
