import Link from "next/link";
import type { RunDelegation } from "../../lib/api";
import { delegationBlockReasonLabel, delegationStatusLabel } from "../../lib/delegation";

type Props = {
  parent?: RunDelegation;
  childRuns: RunDelegation[];
};

export function DelegationTopology({ parent, childRuns }: Props) {
  if (!parent && childRuns.length === 0) {
    return null;
  }
  return (
    <section className="delegation-topology" aria-labelledby="delegation-topology-title">
      <div className="panel-title" id="delegation-topology-title">Child run delegation</div>
      {parent ? (
        <DelegationRow item={parent} label="Parent run" runId={parent.parent_run_id} />
      ) : null}
      {childRuns.map((item) => (
        <DelegationRow item={item} key={item.id} label="Child run" runId={item.child_run_id} />
      ))}
    </section>
  );
}

function DelegationRow({ item, label, runId }: { item: RunDelegation; label: string; runId: string }) {
  return (
    <div className="delegation-row">
      <div className="delegation-identity">
        <span>{label}</span>
        <Link href={`/runs/${encodeURIComponent(runId)}`}>{runId}</Link>
      </div>
      <span className={`replay-status ${item.status}`} aria-label="Delegation status">
        {delegationStatusLabel(item.status)}
      </span>
      <span className="delegation-agent">{item.agent_id}</span>
      {item.summary ? <p>{item.summary}</p> : null}
      {item.error ? <p className="delegation-error">Last execution error: {item.error}</p> : null}
      <div className="delegation-meta">
        <span>Depth {item.depth}</span>
        <span>{item.timeout_ms > 0 ? `${Math.round(item.timeout_ms / 1000)}s timeout` : "No timeout recorded"}</span>
        {item.block_reason ? <span>Recovery reason: {delegationBlockReasonLabel(item.block_reason)}</span> : null}
        {item.summary_truncated ? <span>Summary bounded; full output in child trace</span> : null}
      </div>
    </div>
  );
}
