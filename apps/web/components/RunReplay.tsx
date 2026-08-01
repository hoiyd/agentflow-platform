"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { EpisodeReport, RunReplay as RunReplayData } from "../lib/api";
import { getEpisodeReport, getRunReplay, resumeRun } from "../lib/api";
import { RunUsagePanel } from "./RunUsagePanel";
import {
  EventDetail,
  Metric,
  RetrievalOverview,
  buildRetrievalSummary,
  eventDuration,
  formatDuration,
  formatTokenValue,
  stepDuration
} from "./run-replay/RunEventDetails";

type Props = {
  runId: string;
};

export function RunReplay({ runId }: Props) {
  const router = useRouter();
  const [replay, setReplay] = useState<RunReplayData | null>(null);
  const [episodeReport, setEpisodeReport] = useState<EpisodeReport | null>(null);
  const [selectedEventId, setSelectedEventId] = useState("");
  const [error, setError] = useState("");
  const [isResuming, setIsResuming] = useState(false);
  const hasNavigatedAfterResume = useRef(false);

  useEffect(() => {
    let canceled = false;
    async function load() {
      try {
        setError("");
        const [data, report] = await loadReplayAndReport(runId);
        if (canceled) {
          return;
        }
        setReplay(data);
        setEpisodeReport(report);
        setSelectedEventId(data.run_events[0]?.id ?? "");
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
    () => replay?.run_events.find((event) => event.id === selectedEventId) ?? replay?.run_events[0],
    [replay, selectedEventId]
  );
  const retrievalSummary = useMemo(() => buildRetrievalSummary(replay?.run_events ?? []), [replay?.run_events]);
  const canResumeRecoverable = replay?.run.status === "failed_recoverable";

  async function handleResumeRecoverable() {
    if (!replay || isResuming) {
      return;
    }
    setIsResuming(true);
    hasNavigatedAfterResume.current = false;
    setError("");
    setReplay((current) =>
      current
        ? {
            ...current,
            run: {
              ...current.run,
              status: "running",
              updated_at: new Date().toISOString()
            }
          }
        : current
    );
    try {
      await resumeRun({ run_id: replay.run.id, user_input: "Resume failed recoverable run from replay." }, (event) => {
        if (event.type === "run_state" || event.type === "done") {
          if (event.type === "run_state" && !hasNavigatedAfterResume.current) {
            hasNavigatedAfterResume.current = true;
            router.push(`/workspace?conversation=${encodeURIComponent(event.conversation_id ?? replay.run.conversation_id)}`);
          }
          setReplay((current) =>
            current
              ? {
                  ...current,
                  run: {
                    ...current.run,
                    status: event.status ?? current.run.status,
                    updated_at: new Date().toISOString()
                  }
                }
              : current
          );
        }
      });
      const [data, report] = await loadReplayAndReport(replay.run.id);
      setReplay(data);
      setEpisodeReport(report);
      setSelectedEventId(data.run_events[0]?.id ?? "");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Failed to resume run");
    } finally {
      setIsResuming(false);
    }
  }

  if (error) {
    return (
      <main className="replay-page">
        <Link className="back-link" href="/">
          Back to chat
        </Link>
        <div className="error">{error}</div>
      </main>
    );
  }

  if (!replay) {
    return (
      <main className="replay-page">
        <Link className="back-link" href="/">
          Back to chat
        </Link>
        <div className="empty">Loading run replay...</div>
      </main>
    );
  }

  return (
    <main className="replay-page">
      <header className="replay-header">
        <div>
          <Link className="back-link" href="/">
            Back to chat
          </Link>
          <h1>Run replay</h1>
          <p>{replay.conversation.title}</p>
        </div>
        <div className="replay-header-actions">
          {canResumeRecoverable ? (
            <button className="run-link" disabled={isResuming} onClick={handleResumeRecoverable} type="button">
              Resume run
            </button>
          ) : null}
          <span className={`replay-status ${replay.run.status}`}>{replay.run.status}</span>
        </div>
      </header>
      {canResumeRecoverable ? (
        <section className="recoverable-banner">
          This run stopped unexpectedly and can be resumed from saved collaboration steps.
        </section>
      ) : null}

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

      <RunUsagePanel
        activeRuntimeMS={replay.run.active_runtime_ms ?? 0}
        events={replay.run_events}
        ledger={replay.usage_ledger}
      />

      {episodeReport ? <EpisodeReportPanel report={episodeReport} /> : null}

      <RetrievalOverview summary={retrievalSummary} />

      <section className="replay-grid">
        <div className="timeline-panel">
          <div className="panel-title">Status timeline</div>
          {replay.run_events.length === 0 ? (
            <div className="empty compact">No trace events recorded for this run.</div>
          ) : (
            <div className="timeline-list">
              {replay.run_events.map((event) => (
                <button
                  className={`timeline-event ${event.type} ${event.id === selectedEvent?.id ? "active" : ""}`}
                  key={event.id}
                  onClick={() => setSelectedEventId(event.id)}
                  type="button"
                >
                  <span className="event-type">{event.type}</span>
                  <span className="event-time">{new Date(event.timestamp).toLocaleTimeString()}</span>
				  {eventDuration(event) ? <span className="event-duration">{formatDuration(eventDuration(event))}</span> : null}
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
                const duration = stepDuration(replay.run_events, step.id);
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

function loadReplayAndReport(runId: string) {
  return Promise.all([getRunReplay(runId), getEpisodeReport(runId)]);
}

function EpisodeReportPanel({ report }: { report: EpisodeReport }) {
  const verificationTone =
    report.verification.status === "passed"
      ? "passed"
      : report.verification.status === "failed"
        ? "failed"
        : "needs-review";
  return (
    <section className="episode-report">
      <div className="episode-report-header">
        <div>
          <div className="panel-title inline">Episode report</div>
          <p>
            {report.agent.name} captured {report.steps.length} steps, {report.llm_calls.length} LLM calls,{" "}
            {report.tool_calls.length} tool calls.
          </p>
        </div>
        <button className="run-link" onClick={() => exportEpisodeJSON(report)} type="button">
          Export JSON
        </button>
      </div>

      <div className="episode-report-grid">
        <div className="episode-card">
          <span>Verification</span>
          <strong className={`episode-verification ${verificationTone}`}>{report.verification.status}</strong>
        </div>
        <div className="episode-card">
          <span>Retrieved context</span>
          <strong>{report.retrievals.memories.length + report.retrievals.chunks.length}</strong>
        </div>
        <div className="episode-card">
          <span>Final output</span>
          <strong>{report.final_output ? "Captured" : "Missing"}</strong>
        </div>
      </div>

      <div className="episode-sections">
        <div>
          <div className="episode-section-title">Task</div>
          <p>{report.task || "No task text captured."}</p>
        </div>
        <div>
          <div className="episode-section-title">Evidence</div>
          {report.verification.evidence.length > 0 ? (
            <ul>
              {report.verification.evidence.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          ) : (
            <p>No positive evidence recorded.</p>
          )}
        </div>
        <div>
          <div className="episode-section-title">Warnings</div>
          {report.verification.warnings.length > 0 ? (
            <ul>
              {report.verification.warnings.map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
          ) : (
            <p>No warnings.</p>
          )}
        </div>
        <div>
          <div className="episode-section-title">Errors</div>
          {report.errors.length > 0 ? (
            <ul>
              {report.errors.map((item, index) => (
                <li key={`${item.source}-${index}`}>
                  {item.source}: {item.message}
                </li>
              ))}
            </ul>
          ) : (
            <p>No errors recorded.</p>
          )}
        </div>
      </div>
    </section>
  );
}

function exportEpisodeJSON(report: EpisodeReport) {
  const blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = `agentflow-episode-${report.run.id}.json`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
