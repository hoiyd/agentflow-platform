import { useEffect, useState } from "react";
import { listClarifications, type Clarification } from "../../lib/clarification-api";

type ReadState = {
  runId: string;
  status: string;
  items: Clarification[];
  error: string;
  loaded: boolean;
};

export function useClarifications(runId: string, runStatus: string) {
  const [state, setState] = useState<ReadState>({runId:"", status:"", items:[], error:"", loaded:false});
  const [revision, setRevision] = useState(0);
  useEffect(() => {
    if (!runId) return;
    const controller = new AbortController();
    void listClarifications(runId, controller.signal).then(items => {
      if (!controller.signal.aborted) setState({runId, status:runStatus, items, error:"", loaded:true});
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setState(previous => ({
        runId, status:runStatus, items:previous.runId === runId ? previous.items : [], loaded:true,
        error:error instanceof Error ? error.message : "Failed to load clarification"
      }));
    });
    return () => controller.abort();
  }, [runId, runStatus, revision]);
  const current = state.runId === runId ? state : null;
  const blocking = current?.items.some(q => ["pending", "answered", "expired"].includes(q.status)) ?? false;
  // Waiting may mean a question or plan approval. Do not guess from an old read.
  const loading = Boolean(runId) && (!current?.loaded || current.status !== runStatus);
  return {items:current?.items ?? [], error:current?.error ?? "", loading, blocking, refresh:() => setRevision(n => n + 1)};
}
