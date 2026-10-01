import type { ChatEvent } from "../../lib/api";

export type ReasoningEntry = Omit<Extract<ChatEvent, { type: "model_reasoning" }>, "type"> & { type?: "model_reasoning" };

export function ReasoningDisclosure({ entries, runStatus }: { entries: ReasoningEntry[]; runStatus: string }) {
  if (entries.length === 0) return null;
  return <details className="provider-reasoning">
    <summary>Provider reasoning <span>{entries.length} model {entries.length === 1 ? "call" : "calls"}</span></summary>
    <p className="reasoning-note">Provider-generated output, not verification evidence.</p>
    <ol>
      {entries.map((entry, index) => <li key={`${entry.run_id}:${entry.turn_id}:${entry.model_call_id}`}>
        <header><span>Call {index + 1}</span><span>{statusLabel(entry, runStatus)}</span></header>
        <div className="reasoning-identity">{entry.stage_id ? `Stage ${entry.stage_id} · ` : ""}{entry.model_call_id}</div>
        {entry.text && (entry.status === "complete" || (entry.status === "receiving" && !runStopped(runStatus))) ? <pre>{entry.text}</pre> : null}
        {entry.truncated ? <p className="reasoning-note">Display truncated; model continuation is unchanged.</p> : null}
      </li>)}
    </ol>
  </details>;
}

function statusLabel(entry: ReasoningEntry, runStatus: string) {
  if (entry.status === "complete") return "Received";
  if (entry.status === "interrupted") return "Interrupted; text withheld";
  if (runStatus === "canceled") return "Run canceled; text withheld";
  if (runStopped(runStatus)) return "Call ended; text unavailable";
  return "Receiving provider reasoning";
}

function runStopped(status: string) {
  return ["canceled", "failed", "failed_recoverable", "completed", "waiting_for_user"].includes(status);
}
