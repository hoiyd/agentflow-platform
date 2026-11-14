import Link from "next/link";
import { Check, ShieldCheck, X } from "lucide-react";
import type { ToolApproval, ToolApprovalDecision } from "../../lib/tool-approval-api";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";

type Props = {
  items: ToolApproval[];
  busy?: boolean;
  recoverable?: boolean;
  conversationHref?: string;
  onDecision?: (item: ToolApproval, decision: ToolApprovalDecision["decision"] | "resume") => void;
};

const labels: Record<string, string> = {
  pending: "Approval required", approved: "Approved, not yet executed", consumed: "Execution claim consumed",
  claimed: "Execution claimed", rejected: "Rejected", revoked: "Approval revoked", expired: "Approval expired", canceled: "Run canceled"
};

export function ToolApprovalPanel({ items, busy, recoverable, conversationHref, onDecision }: Props) {
  const readOnly = useWorkspaceReadOnly();
  if (!items.length) return null;
  return <section className="tool-approval-panel" aria-label="Tool approvals">
    {items.map((item, index) => {
      const active = index === items.length - 1;
      const resumable = item.status === "approved" || recoverable && ["claimed", "consumed"].includes(item.status);
      const actionable = active && (item.status === "pending" || resumable);
      return <article key={item.id}>
        <header><ShieldCheck aria-hidden="true" size={17} /><strong>{labels[item.status] ?? item.status}</strong><code>{item.tool}</code></header>
        <p className="tool-approval-target">{String(item.arguments.root ?? "")} / {String(item.arguments.path ?? "")}</p>
        <dl className="tool-approval-change">
          <div><dt>Replace</dt><dd>{String(item.arguments.old_text ?? "")}</dd></div>
          <div><dt>With</dt><dd>{String(item.arguments.new_text ?? "")}</dd></div>
        </dl>
        <details><summary>Exact call and approval boundary</summary>
          <pre>{JSON.stringify({ arguments: item.arguments, call: item.tool_call_id, turn: item.turn_id, stage: item.stage_id,
            arguments_hash: item.arguments_hash, definition: item.definition_revision, policy: item.policy_hash, snapshot: item.snapshot_hash }, null, 2)}</pre>
        </details>
        <footer><span>Expires {new Date(item.expires_at).toLocaleString()}</span>
          {actionable && onDecision ? <div className="tool-approval-actions">
            {["pending", "approved"].includes(item.status) ? <button className="secondary-action" type="button" disabled={busy || readOnly} onClick={() => onDecision(item, item.status === "pending" ? "reject" : "revoke")}>
              <X size={14} aria-hidden="true" />{item.status === "pending" ? "Reject" : "Revoke"}
            </button> : null}
            <button className="tool-approval-confirm" type="button" disabled={busy || readOnly} onClick={() => onDecision(item, item.status === "pending" ? "approve" : "resume")}>
              <Check size={14} aria-hidden="true" />{item.status === "pending" ? "Approve once & continue" : "Continue approved call"}
            </button>
          </div> : actionable && conversationHref ? <Link href={conversationHref}>Review approval in chat</Link> : null}
        </footer>
      </article>;
    })}
  </section>;
}
