import { listToolApprovals } from "../../lib/tool-approval-api";
import { useRunInterruptRecords } from "./useRunInterruptRecords";

export function useToolApprovals(runId: string, status: string) {
  return useRunInterruptRecords(runId, status, listToolApprovals, "Failed to load Tool approvals");
}
