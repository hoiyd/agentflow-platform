import { listClarifications } from "../../lib/clarification-api";
import { useRunInterruptRecords } from "./useRunInterruptRecords";

export function useClarifications(runId: string, runStatus: string) {
  const records = useRunInterruptRecords(runId, runStatus, listClarifications, "Failed to load clarification");
  return { ...records, blocking: records.items.some(q => ["pending", "answered", "expired"].includes(q.status)) };
}
