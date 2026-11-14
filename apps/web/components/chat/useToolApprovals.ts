import { useEffect, useMemo, useRef, useState } from "react";
import { listToolApprovals, type ToolApproval } from "../../lib/tool-approval-api";

export function useToolApprovals(runId: string, status: string) {
  const [revision, setRevision] = useState(0);
  const requested = useRef(0);
  const request = useMemo(() => ({ runId, status, revision }), [runId, status, revision]);
  const [state, setState] = useState<{ request: typeof request | null; items: ToolApproval[]; error: string }>({ request: null, items: [], error: "" });
  useEffect(() => {
    if (!request.runId) return;
    const controller = new AbortController();
    void listToolApprovals(request.runId, controller.signal).then(items => {
      if (!controller.signal.aborted) setState({ request, items, error: "" });
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setState(previous => ({
        request, items: previous.request?.runId === request.runId ? previous.items : [],
        error: error instanceof Error ? error.message : "Failed to load Tool approvals"
      }));
    });
    return () => controller.abort();
  }, [request]);
  const current = state.request?.runId === runId ? state : null;
  const loading = Boolean(runId) && current?.request !== request;
  const error = current?.error ?? "";
  const ready = Boolean(runId) && !loading && !error;
  return {
    items: current?.items ?? [], loading, error, ready,
    blocking: current?.items.some(item => ["pending", "approved", "expired", "claimed"].includes(item.status) || status === "failed_recoverable" && item.status === "consumed") ?? false,
    isReady: (id = runId, runStatus = status) => ready && id === runId && status === runStatus && revision === requested.current,
    refresh: () => { requested.current += 1; setRevision(requested.current); }
  };
}
