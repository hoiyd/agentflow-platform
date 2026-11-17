import { listClarifications } from "../../lib/clarification-api";
import { useRunInterruptRecords } from "./useRunInterruptRecords";

export function useClarifications(runId: string, runStatus: string) {
  return useRunInterruptRecords(runId, runStatus, listClarifications, "Failed to load clarification");
}
