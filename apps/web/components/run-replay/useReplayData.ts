import { useEffect, useRef, useState } from "react";
import type { RunReplay } from "../../lib/api";
import { createLatestRequestController } from "../../lib/latest-request";
import { getReplayPageData, type ReplayPageData } from "../../lib/replay-page-data";

type ReadState = {
  runId: string;
  page: ReplayPageData | null;
  loadError: string;
  refreshError: string;
};

// Read failures never replace previously accepted evidence with an error page.
export function useReplayData(runId: string) {
  const [state, setState] = useState<ReadState>({ runId, page: null, loadError: "", refreshError: "" });
  const [requests] = useState(createLatestRequestController);
  const mounted = useRef(false);

  function read(initial: boolean) {
    if (!mounted.current) return Promise.resolve();
    const request = requests.begin();
    return getReplayPageData(runId, request.signal).then(page => {
      if (!request.isCurrent()) return;
      if (page.data.run.id !== runId) throw new Error("Replay response belongs to another Run");
      setState({ runId, page, loadError: "", refreshError: "" });
    }).catch((error: unknown) => {
      if (!request.isCurrent()) return;
      const message = error instanceof Error ? error.message : "Failed to load run replay";
      setState(current => initial
        ? { runId, page: null, loadError: message, refreshError: "" }
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
    setState(current => current.runId === runId && current.page ? {
      ...current, page: { ...current.page, data: { ...current.page.data,
        run: { ...current.page.data.run, status, updated_at: new Date().toISOString() }
      } }
    } : current);
  }

  const current = state.runId === runId ? state : null;
  return {
    replay: current?.page?.data ?? null,
    episodeReport: current?.page?.report ?? null,
    episodeReportError: current?.page?.reportError ?? "",
    loadError: current?.loadError ?? "",
    refreshError: current?.refreshError ?? "",
    refresh: () => read(false),
    invalidateReads: requests.cancel,
    updateRunStatus
  };
}
