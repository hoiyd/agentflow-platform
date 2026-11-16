import { listToolApprovals } from "../../lib/tool-approval-api";
import { useRunInterruptRecords } from "./useRunInterruptRecords";

export function useToolApprovals(runId: string, status: string) {
  const records = useRunInterruptRecords(runId, status, listToolApprovals, "Failed to load Tool approvals");
  return {
    ...records,
    blocking: records.items.some(item => ["pending", "approved", "expired", "claimed"].includes(item.status) || status === "failed_recoverable" && item.status === "consumed")
  };
}
