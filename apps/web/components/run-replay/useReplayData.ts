import { useEffect, useRef, useState } from "react";
import { getRunReplay, type RunReplay } from "../../lib/api";
import { createLatestRequestController } from "../../lib/latest-request";

type ReadState = {
  runId: string;
  data: RunReplay | null;
  loadError: string;
  refreshError: string;
};

// Read failures never replace previously accepted evidence with an error page.
export function useReplayData(runId: string) {
  const [state, setState] = useState<ReadState>({ runId, data: null, loadError: "", refreshError: "" });
  const [requests] = useState(createLatestRequestController);
  const mounted = useRef(false);

  function read(initial: boolean) {
    if (!mounted.current) return Promise.resolve();
    const request = requests.begin();
    return getRunReplay(runId, request.signal).then(data => {
      if (!request.isCurrent()) return;
      if (data.run.id !== runId) throw new Error("Replay response belongs to another Run");
      setState({ runId, data, loadError: "", refreshError: "" });
    }).catch((error: unknown) => {
      if (!request.isCurrent()) return;
      const message = error instanceof Error ? error.message : "Failed to load run replay";
      setState(current => initial
        ? { runId, data: null, loadError: message, refreshError: "" }
        : current.runId === runId ? { ...current, refreshError: message }
        : current);
    });
  }

  useEffect(() => {
    mounted.current = true;
    void read(true);
    return () => {
      mounted.current = false;
      requests.cancel();
    };
  }, [runId, requests]); // eslint-disable-line react-hooks/exhaustive-deps

  function updateRunStatus(status: RunReplay["run"]["status"]) {
    // A read started before this event cannot overwrite the accepted status.
    requests.cancel();
    setState(current => current.runId === runId && current.data ? {
      ...current, data: { ...current.data,
        run: { ...current.data.run, status, updated_at: new Date().toISOString() }
      }
    } : current);
  }

  const current = state.runId === runId ? state : null;
  return {
    replay: current?.data ?? null,
    loadError: current?.loadError ?? "",
    refreshError: current?.refreshError ?? "",
    refresh: () => read(false),
    invalidateReads: requests.cancel,
    updateRunStatus
  };
}
