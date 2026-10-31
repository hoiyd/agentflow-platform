import { useEffect, useMemo, useRef, useState } from "react";
import { listClarifications, type Clarification } from "../../lib/clarification-api";

type ReadRequest = {runId: string; status: string; revision: number};
type ReadState = {
  request: ReadRequest | null;
  items: Clarification[];
  error: string;
};

export function useClarifications(runId: string, runStatus: string) {
  const [state, setState] = useState<ReadState>({request:null, items:[], error:""});
  const [revision, setRevision] = useState(0);
  const requestedRevision = useRef(0);
  const request = useMemo(() => ({runId, status:runStatus, revision}), [runId, runStatus, revision]);
  useEffect(() => {
    if (!request.runId) return;
    const controller = new AbortController();
    void listClarifications(request.runId, controller.signal).then(items => {
      if (!controller.signal.aborted) setState({request, items, error:""});
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setState(previous => ({
        request, items:previous.request?.runId === request.runId ? previous.items : [],
        error:error instanceof Error && error.message ? error.message : "Failed to load clarification"
      }));
    });
    return () => controller.abort();
  }, [request]);
  const current = state.request?.runId === runId ? state : null;
  const blocking = current?.items.some(q => ["pending", "answered", "expired"].includes(q.status)) ?? false;
  // Waiting may mean a question or plan approval. Do not guess from an old read.
  const loading = Boolean(runId) && current?.request !== request;
  const error = current?.error ?? "";
  const ready = Boolean(runId) && !loading && !error;
  return {items:current?.items ?? [], error, loading, ready, blocking,
    isReady:(currentRunId = runId, currentStatus = runStatus) => ready && currentRunId === runId && currentStatus === runStatus && revision === requestedRevision.current,
    refresh:() => { requestedRevision.current += 1; setRevision(requestedRevision.current); }};
}
