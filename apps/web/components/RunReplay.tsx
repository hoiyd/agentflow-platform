"use client";

import { useEffect, useMemo, useState } from "react";
import type { RunReplay as RunReplayData, TraceEventInfo } from "../lib/api";
import { getRunReplay } from "../lib/api";

type Props = {
  runId: string;
};

export function RunReplay({ runId }: Props) {
  const [replay, setReplay] = useState<RunReplayData | null>(null);
  const [selectedEventId, setSelectedEventId] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    let canceled = false;
    async function load() {
      try {
        setError("");
        const data = await getRunReplay(runId);
        if (canceled) {
          return;
        }
        setReplay(data);
        setSelectedEventId(data.events[0]?.id ?? "");
      } catch (err) {
        if (!canceled) {
          setError(err instanceof Error ? err.message : "Failed to load run replay");
        }
      }
    }
    void load();
    return () => {
      canceled = true;
    };
  }, [runId]);

  const selectedEvent = useMemo(
    () => replay?.events.find((event) => event.id === selectedEventId) ?? replay?.events[0],
    [replay, selectedEventId]
  );

  if (error) {
    return (
      <main className="replay-page">
        <a className="back-link" href="/">
          Back to chat
        </a>
        <div className="error">{error}</div>
      </main>
    );
  }

  if (!replay) {
    return (
      <main className="replay-page">
        <a className="back-link" href="/">
          Back to chat
        </a>
        <div className="empty">Loading run replay...</div>
      </main>
    );
  }

  return (
    <main className="replay-page">
      <header className="replay-header">
        <div>
          <a className="back-link" href="/">
            Back to chat
          </a>
          <h1>Run replay</h1>
          <p>{replay.conversation.title}</p>
        </div>
        <span className={`replay-status ${replay.run.status}`}>{replay.run.status}</span>
      </header>

      <section className="replay-summary">
        <Metric label="Total duration" value={formatDuration(replay.summary.total_duration_ms)} />
        <Metric
          label="Total token"
          value={formatTokenValue(replay.summary.total_tokens, replay.summary.token_usage_estimated)}
        />
        <Metric label="LLM calls" value={String(replay.summary.llm_calls)} />
        <Metric label="Tool calls" value={String(replay.summary.tool_calls)} />
        <Metric label="Errors" value={String(replay.summary.error_count)} tone={replay.summary.error_count > 0 ? "danger" : ""} />
      </section>

      <section className="replay-grid">
        <div className="timeline-panel">
          <div className="panel-title">Status timeline</div>
          {replay.events.length === 0 ? (
            <div className="empty compact">No trace events recorded for this run.</div>
          ) : (
            <div className="timeline-list">
              {replay.events.map((event) => (
                <button
                  className={`timeline-event ${event.type} ${event.id === selectedEvent?.id ? "active" : ""}`}
                  key={event.id}
                  onClick={() => setSelectedEventId(event.id)}
                  type="button"
                >
                  <span className="event-type">{event.type}</span>
                  <span className="event-time">{new Date(event.timestamp).toLocaleTimeString()}</span>
                  {event.duration_ms ? <span className="event-duration">{formatDuration(event.duration_ms)}</span> : null}
                </button>
              ))}
            </div>
          )}
        </div>

        <div className="step-panel">
          <div className="panel-title">Step latency</div>
          {replay.steps.length === 0 ? (
            <div className="empty compact">This run has no collaboration steps.</div>
          ) : (
            <div className="step-list">
              {replay.steps.map((step) => {
                const duration = stepDuration(replay.events, step.id);
                return (
                  <article className="step-row" key={step.id}>
                    <div>
                      <div className="step-name">
                        {step.iteration ? `#${step.iteration} ` : ""}
                        {step.role}
                      </div>
                      <div className="step-agent">{step.agent_id || "system"}</div>
                    </div>
                    <div className="step-meta">
                      <span>{step.status}</span>
                      <strong>{duration ? formatDuration(duration) : "n/a"}</strong>
                    </div>
                  </article>
                );
              })}
            </div>
          )}
        </div>

        <div className="detail-panel">
          <div className="panel-title">Event detail</div>
          {selectedEvent ? (
            <EventDetail event={selectedEvent} />
          ) : (
            <div className="empty compact">Select a timeline event.</div>
          )}
        </div>
      </section>
    </main>
  );
}

function Metric({ label, value, tone = "" }: { label: string; value: string; tone?: string }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function EventDetail({ event }: { event: TraceEventInfo }) {
  const payload = event.payload ?? {};
  const isEstimated = payload.token_usage_estimated === true;
  return (
    <div className="event-detail">
      <div className="detail-kv">
        <span>Type</span>
        <strong>{event.type}</strong>
      </div>
      <div className="detail-kv">
        <span>Timestamp</span>
        <strong>{new Date(event.timestamp).toLocaleString()}</strong>
      </div>
      {event.duration_ms ? (
        <div className="detail-kv">
          <span>Duration</span>
          <strong>{formatDuration(event.duration_ms)}</strong>
        </div>
      ) : null}
      {"prompt_tokens" in payload || "completion_tokens" in payload || "total_tokens" in payload ? (
        <div className="token-strip">
          <Metric label="Prompt" value={formatTokenValue(payload.prompt_tokens, isEstimated)} />
          <Metric label="Completion" value={formatTokenValue(payload.completion_tokens, isEstimated)} />
          <Metric label="Total" value={formatTokenValue(payload.total_tokens, isEstimated)} />
        </div>
      ) : null}
      <pre>{JSON.stringify(payload, null, 2)}</pre>
    </div>
  );
}

function formatTokenValue(value: unknown, estimated: boolean) {
  const numberValue = typeof value === "number" ? value : Number(value ?? 0);
  return `${Number.isFinite(numberValue) ? numberValue : 0}${estimated ? " est." : ""}`;
}

function stepDuration(events: TraceEventInfo[], stepId: string) {
  return events
    .filter((event) => event.step_id === stepId && typeof event.duration_ms === "number")
    .reduce((total, event) => total + (event.duration_ms ?? 0), 0);
}

function formatDuration(durationMS: number) {
  if (!durationMS) {
    return "0 ms";
  }
  if (durationMS < 1000) {
    return `${durationMS} ms`;
  }
  return `${(durationMS / 1000).toFixed(2)} s`;
}
