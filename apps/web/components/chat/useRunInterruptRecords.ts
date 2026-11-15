import { useEffect, useMemo, useRef, useState } from "react";

type ReadRequest = { runId: string; status: string; revision: number };

// Clarification and approval reads share freshness, not authorization semantics.
export function useRunInterruptRecords<T>(runId: string, status: string,
  load: (id: string, signal: AbortSignal) => Promise<T[]>, fallbackError: string) {
  const [revision, setRevision] = useState(0);
  const requested = useRef(0);
  const request = useMemo(() => ({ runId, status, revision }), [runId, status, revision]);
  const [state, setState] = useState<{ request: ReadRequest | null; items: T[]; error: string }>({ request: null, items: [], error: "" });
  useEffect(() => {
    if (!request.runId) return;
    const controller = new AbortController();
    void load(request.runId, controller.signal).then(items => {
      if (!controller.signal.aborted) setState({ request, items, error: "" });
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setState(previous => ({
        request, items: previous.request?.runId === request.runId ? previous.items : [],
        error: error instanceof Error && error.message ? error.message : fallbackError
      }));
    });
    return () => controller.abort();
  }, [request, load, fallbackError]);
  const current = state.request?.runId === runId ? state : null;
  const loading = Boolean(runId) && current?.request !== request;
  const error = current?.error ?? "";
  const ready = Boolean(runId) && !loading && !error;
  return {
    items: current?.items ?? [], loading, error, ready,
    isReady: (id = runId, runStatus = status) => ready && id === runId && status === runStatus && revision === requested.current,
    refresh: () => { requested.current += 1; setRevision(requested.current); }
  };
}
