import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Play } from "lucide-react";
import { streamChat } from "../../lib/api";
import type { RunReplay } from "../../lib/api-types";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";

export function PlanningPanel({ replay }: { replay: RunReplay }) {
  const router = useRouter();
  const readOnly = useWorkspaceReadOnly();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [openedAt] = useState(() => Date.now());
  const pending = useRef<AbortController | null>(null);
  useEffect(() => () => pending.current?.abort(), []);
  const plan = replay.plan;
  const restriction = replay.runtime_snapshot?.execution_constraint;
  const origin = replay.runtime_snapshot?.plan_origin as { run_id?: string } | undefined;
  if (restriction !== "plan_only") return origin?.run_id
    ? <p className="planning-origin">Execution of <Link href={`/runs/${encodeURIComponent(origin.run_id)}`}>reviewed plan</Link></p> : null;
  const expired = Boolean(plan && Date.parse(plan.expires_at) <= openedAt);
  async function start() {
    if (!plan || pending.current) return;
    const controller = new AbortController();
    pending.current = controller;
    setBusy(true); setError("");
    try {
      await streamChat({ conversation_id: replay.run.conversation_id, message: "Execute reviewed plan", plan_origin: { run_id: plan.run_id, hash: plan.hash } }, event => {
        if (!controller.signal.aborted && event.type === "run_state") router.push(`/workspace?conversation=${encodeURIComponent(replay.run.conversation_id)}`);
      }, controller.signal);
    } catch (err) {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : "Failed to start execution");
    } finally {
      if (!controller.signal.aborted) { pending.current = null; setBusy(false); }
    }
  }
  return <section className="planning-panel" aria-labelledby="planning-heading">
    <header><h2 id="planning-heading">Read-only plan</h2><span>No write authority</span>
      <button className="secondary-action" type="button" onClick={() => void start()} disabled={readOnly || busy || expired || !plan?.sources.length || replay.run.status !== "completed"}><Play size={14} />{busy ? "Starting execution..." : "Start execution"}</button>
    </header>
    {error ? <div className="error" role="alert">{error}</div> : null}
    {plan ? <>
      <details><summary>Proposal</summary><p className="planning-proposal">{plan.proposal}</p></details>
      <ul>{plan.sources.map(source => <li key={`${source.root}/${source.path}`}><span>{source.root}/{source.path}</span><code>{source.content_hash}</code></li>)}</ul>
      {plan.sources.length === 0 ? <p>No file sources recorded. Execution unavailable.</p> : null}
      <div className="planning-metadata"><code>{plan.hash}</code><span>{expired ? "Expired" : `Valid until ${new Date(plan.expires_at).toLocaleString()}`}</span></div>
    </> : <p>No completed proposal recorded.</p>}
  </section>;
}
