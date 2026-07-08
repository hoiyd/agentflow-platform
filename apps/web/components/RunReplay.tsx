"use client";

import { useEffect, useMemo, useState } from "react";
import type { EpisodeReport, RunReplay as RunReplayData, TraceEventInfo } from "../lib/api";
import { getEpisodeReport, getRunReplay } from "../lib/api";

type Props = {
  runId: string;
};

type RetrievedMemoryPayload = {
  id?: string;
  kind?: string;
  content?: string;
  similarity?: number;
  score?: number;
  metadata?: Record<string, unknown>;
};

type RetrievedChunkPayload = {
  document_id?: string;
  document_title?: string;
  chunk_id?: string;
  chunk_index?: number;
  content?: string;
  similarity?: number;
  score?: number;
  rerank_score?: number;
  lexical_boost?: number;
  metadata_boost?: number;
  metadata?: Record<string, unknown>;
};

export function RunReplay({ runId }: Props) {
  const [replay, setReplay] = useState<RunReplayData | null>(null);
  const [episodeReport, setEpisodeReport] = useState<EpisodeReport | null>(null);
  const [selectedEventId, setSelectedEventId] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    let canceled = false;
    async function load() {
      try {
        setError("");
        const [data, report] = await Promise.all([getRunReplay(runId), getEpisodeReport(runId)]);
        if (canceled) {
          return;
        }
        setReplay(data);
        setEpisodeReport(report);
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
  const retrievalSummary = useMemo(() => buildRetrievalSummary(replay?.events ?? []), [replay?.events]);

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

      {episodeReport ? <EpisodeReportPanel report={episodeReport} /> : null}

      <RetrievalOverview summary={retrievalSummary} />

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
  const memories = retrievedMemories(payload);
  const chunks = retrievedChunks(payload);
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
      {stringPayload(payload, "executor") ? (
        <div className="detail-kv">
          <span>Executor</span>
          <strong>{stringPayload(payload, "executor")}</strong>
        </div>
      ) : null}
      {stringPayload(payload, "framework") ? (
        <div className="detail-kv">
          <span>Framework</span>
          <strong>{stringPayload(payload, "framework")}</strong>
        </div>
      ) : null}
      {stringPayload(payload, "agent_name") || stringPayload(payload, "agent_id") ? (
        <div className="detail-kv">
          <span>Agent</span>
          <strong>{stringPayload(payload, "agent_name") || stringPayload(payload, "agent_id")}</strong>
        </div>
      ) : null}
      {"memory_enabled" in payload ? (
        <div className="detail-kv">
          <span>Memory</span>
          <strong>{payload.memory_enabled === false ? "Disabled" : "Enabled"}</strong>
        </div>
      ) : null}
      {"retrieval_enabled" in payload ? (
        <div className="detail-kv">
          <span>Knowledge</span>
          <strong>{payload.retrieval_enabled === false ? "Disabled" : "Enabled"}</strong>
        </div>
      ) : null}
      {Array.isArray(payload.configured_tools) ? (
        <div className="detail-kv">
          <span>Tools</span>
          <strong>{payload.configured_tools.length > 0 ? payload.configured_tools.join(", ") : "None"}</strong>
        </div>
      ) : null}
      {"prompt_tokens" in payload || "completion_tokens" in payload || "total_tokens" in payload ? (
        <div className="token-strip">
          <Metric label="Prompt" value={formatTokenValue(payload.prompt_tokens, isEstimated)} />
          <Metric label="Completion" value={formatTokenValue(payload.completion_tokens, isEstimated)} />
          <Metric label="Total" value={formatTokenValue(payload.total_tokens, isEstimated)} />
        </div>
      ) : null}
      {memories.length > 0 || chunks.length > 0 ? <RetrievedContext memories={memories} chunks={chunks} /> : null}
      <section className="raw-json-panel">
        <div className="raw-json-title">
          <span>Raw event payload</span>
          <small>Full trace JSON</small>
        </div>
        <pre>{JSON.stringify(payload, null, 2)}</pre>
      </section>
    </div>
  );
}

function RetrievalOverview({ summary }: { summary: ReturnType<typeof buildRetrievalSummary> }) {
  if (summary.eventCount === 0) {
    return (
      <section className="retrieval-overview">
        <div>
          <div className="panel-title inline">Retrieved context</div>
          <p>No retrieval trace events recorded for this run.</p>
        </div>
      </section>
    );
  }
  return (
    <section className="retrieval-overview">
      <div>
        <div className="panel-title inline">Retrieved context</div>
        <p>
          {summary.eventCount} retrieval events, {summary.memoryCount} memories, {summary.chunkCount} knowledge chunks.
        </p>
      </div>
      <div className="retrieval-model">
        <span>Embedding</span>
        <strong>{summary.embeddingLabel}</strong>
      </div>
      <div className="retrieval-model">
        <span>Executor</span>
        <strong>{summary.executorLabel}</strong>
      </div>
    </section>
  );
}

function RetrievedContext({ memories, chunks }: { memories: RetrievedMemoryPayload[]; chunks: RetrievedChunkPayload[] }) {
  return (
    <div className="retrieved-context">
      {memories.length > 0 ? (
        <div>
          <div className="retrieved-context-title">Retrieved memories</div>
          <div className="retrieved-list">
            {memories.map((memory, index) => (
              <article className="retrieved-item" key={`${memory.id ?? "memory"}-${index}`}>
                <div className="retrieved-item-header">
                  <strong>{memory.kind || "memory"}</strong>
                  <span>{formatScore(memory.score ?? memory.similarity)}</span>
                </div>
                <p>{memory.content}</p>
                <small>{memory.id}</small>
              </article>
            ))}
          </div>
        </div>
      ) : null}
      {chunks.length > 0 ? (
        <div>
          <div className="retrieved-context-title">Retrieved knowledge chunks</div>
          <div className="retrieved-list">
            {chunks.map((chunk, index) => (
              <article className="retrieved-item" key={`${chunk.chunk_id ?? "chunk"}-${index}`}>
                <div className="retrieved-item-header">
                  <strong>
                    {chunk.document_title || "Untitled document"}
                    {typeof chunk.chunk_index === "number" ? ` #${chunk.chunk_index + 1}` : ""}
                  </strong>
                  <span>{formatScore(chunk.rerank_score ?? chunk.score ?? chunk.similarity)}</span>
                </div>
                <p>{chunk.content}</p>
                <small>{[chunk.chunk_id, chunk.lexical_boost ? `lexical +${chunk.lexical_boost.toFixed(3)}` : ""].filter(Boolean).join(" - ")}</small>
              </article>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function buildRetrievalSummary(events: TraceEventInfo[]) {
  const explicitEvents = events.filter((event) => event.type === "retrieval");
  const retrievalEvents =
    explicitEvents.length > 0
      ? explicitEvents
      : events.filter((event) => retrievedMemories(event.payload).length > 0 || retrievedChunks(event.payload).length > 0);
  const memoryCount = retrievalEvents.reduce((total, event) => total + retrievedMemories(event.payload).length, 0);
  const chunkCount = retrievalEvents.reduce((total, event) => total + retrievedChunks(event.payload).length, 0);
  const firstPayload = retrievalEvents[0]?.payload ?? {};
  const provider = stringPayload(firstPayload, "embedding_provider");
  const model = stringPayload(firstPayload, "embedding_model");
  const dimensions = numberPayload(firstPayload, "embedding_dimensions");
  const executor = firstNonEmpty(events.map((event) => stringPayload(event.payload, "executor")));
  const framework = firstNonEmpty(events.map((event) => stringPayload(event.payload, "framework")));
  return {
    eventCount: retrievalEvents.length,
    memoryCount,
    chunkCount,
    embeddingLabel: provider || model ? [provider, model, dimensions ? `${dimensions}d` : ""].filter(Boolean).join(" / ") : "not recorded",
    executorLabel: executor || framework ? [executor, framework].filter(Boolean).join(" / ") : "not recorded"
  };
}

function retrievedMemories(payload: Record<string, unknown>): RetrievedMemoryPayload[] {
  return arrayPayload<RetrievedMemoryPayload>(payload.retrieved_memories);
}

function retrievedChunks(payload: Record<string, unknown>): RetrievedChunkPayload[] {
  return arrayPayload<RetrievedChunkPayload>(payload.retrieved_chunks);
}

function arrayPayload<T>(value: unknown): T[] {
  return Array.isArray(value) ? (value as T[]) : [];
}

function stringPayload(payload: Record<string, unknown>, key: string) {
  const value = payload[key];
  return typeof value === "string" ? value : "";
}

function numberPayload(payload: Record<string, unknown>, key: string) {
  const value = payload[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function firstNonEmpty(values: string[]) {
  return values.find((value) => value.trim()) ?? "";
}

function formatScore(value: unknown) {
  const numberValue = typeof value === "number" ? value : Number(value ?? 0);
  return Number.isFinite(numberValue) ? numberValue.toFixed(3) : "n/a";
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
