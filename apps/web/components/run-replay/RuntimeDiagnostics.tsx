import { AlertTriangle, LocateFixed } from "lucide-react";
import type { RuntimeInvariantFailure } from "../../lib/api";

type Props = {
  asOfSequence: number;
  failures: RuntimeInvariantFailure[];
  onInspectEvent: (eventId: string) => void;
};

export function RuntimeDiagnostics({ asOfSequence, failures, onInspectEvent }: Props) {
  if (failures.length === 0) {
    return null;
  }

  return (
    <section aria-labelledby="runtime-diagnostics-title" className="runtime-diagnostics">
      <header className="runtime-diagnostics-header">
        <div className="runtime-diagnostics-heading">
          <AlertTriangle aria-hidden="true" size={16} strokeWidth={1.8} />
          <div>
            <h2 id="runtime-diagnostics-title">Runtime diagnostics</h2>
            <p>
              {failures.length} protocol {failures.length === 1 ? "violation" : "violations"} detected
            </p>
          </div>
        </div>
        <span className="runtime-diagnostics-watermark">As of event {asOfSequence}</span>
      </header>

      <div className="runtime-diagnostics-list">
        {failures.map((failure, index) => {
          const eventId = failure.event_id;
          return (
            <article
              className="runtime-diagnostic"
              key={`${failure.owner}-${failure.code}-${eventId ?? failure.sequence ?? "run"}-${index}`}
            >
              <div className="runtime-diagnostic-identity">
                <code>{failure.code}</code>
                <span>{failure.owner}</span>
              </div>
              <div className="runtime-diagnostic-location">
                <span>{failure.sequence ? `Sequence ${failure.sequence}` : "Run scope"}</span>
                {eventId ? <code title={eventId}>{eventId}</code> : null}
              </div>
              <p>{failure.message}</p>
              {eventId ? (
                <button className="runtime-diagnostic-action" onClick={() => onInspectEvent(eventId)} type="button">
                  <LocateFixed aria-hidden="true" size={14} strokeWidth={1.8} />
                  Inspect event
                </button>
              ) : null}
            </article>
          );
        })}
      </div>
    </section>
  );
}
