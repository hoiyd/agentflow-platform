import type { ToolProgress } from "../../lib/api-types";

// Shared by Chat and Replay. Saved progress is not a resume point or a result;
// only the actual Tool lifecycle may declare completion.
export function ToolProgressPanel({ items, runStatus }: { items: ToolProgress[]; runStatus: string }) {
  if (!items.length) return null;
  const stopped = !["running", "queued", "canceling"].includes(runStatus);
  return <section className="tool-progress-panel" aria-label="Tool progress">
    <details>
      <summary>Tool progress <span>{items.length} {items.length === 1 ? "call" : "calls"} · {items.at(-1)?.phase.replaceAll("_", " ")}</span></summary>
      <div className="tool-progress-items">
        {items.map(item => {
          const status = item.status === "running" && stopped ? "interrupted" : item.status;
          return <article key={`${item.turn_id}:${item.tool_call_id}`}>
            <header><strong>{item.tool_name}</strong><span>{status === "interrupted" ? "Incomplete" : status === "running" ? "Running" : status === "completed" ? "Completed" : "Failed"}</span></header>
            <div className="tool-progress-phase"><span>{item.phase.replaceAll("_", " ")}</span>
              {item.completed != null && item.total != null ? <span>{item.completed} / {item.total}</span> : null}</div>
            {item.message ? <p>{item.message}</p> : null}
            <small>Saved at event {item.sequence}{item.truncated ? " · Display truncated" : ""}</small>
          </article>;
        })}
      </div>
    </details>
  </section>;
}
