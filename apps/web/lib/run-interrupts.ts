import type { Clarification } from "./clarification-api";
import type { ToolApproval } from "./tool-approval-api";

export function blocksClarification(item: Pick<Clarification, "status">): boolean {
  return ["pending", "answered", "expired"].includes(item.status);
}

export function blocksToolApproval(item: Pick<ToolApproval, "status">, runStatus: string): boolean {
  return ["pending", "approved", "expired", "claimed"].includes(item.status)
    || runStatus === "failed_recoverable" && item.status === "consumed";
}

export function selectRunInterrupt(questions: readonly Clarification[], approvals: readonly ToolApproval[], runStatus: string) {
  // Both APIs return creation order; a newer clarification supersedes old approval recovery.
  const question = questions.at(-1), approval = approvals.at(-1);
  const approvalIsLatest = !question || Boolean(approval && Date.parse(approval.created_at) >= Date.parse(question.created_at));
  const kind = approvalIsLatest && approvals.some(item => blocksToolApproval(item, runStatus)) ? "tool_approval"
    : questions.some(blocksClarification) ? "clarification" : null;
  return { kind, approvalIsLatest };
}
