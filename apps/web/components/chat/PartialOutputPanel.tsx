import type { PartialOutput } from "../../lib/api-types";

// Display recovery only: never insert provisional text into conversation Messages.
export function PartialOutputPanel({ outputs, runStatus }: { outputs: PartialOutput[]; runStatus: string }) {
  if (runStatus === "completed") return null;
  const visible = outputs.filter(item => item.text && item.status !== "retracted");
  if (!visible.length) return null;
  const stopped = ["canceled", "failed", "failed_recoverable"].includes(runStatus);
  return <section className="partial-output" aria-label="Recovered output">
    {visible.map(item => <article key={`${item.turn_id}:${item.channel}:${item.model_call_id}`}>
      <header><strong>{item.channel === "reasoning" ? "Provider reasoning" : item.stage_id ? `${item.role || "Stage"} draft` : "Partial answer"}</strong>
        <span>{stopped || item.status === "interrupted" ? "Incomplete" : item.status === "final" ? "Stage output" : "Provisional"}</span></header>
      <small>Saved at event {item.sequence}{item.stage_id ? ` · Stage ${item.stage_id}` : ""}</small>
      <pre>{item.text}</pre>
      {item.truncated ? <small>Saved display truncated</small> : null}
    </article>)}
  </section>;
}
