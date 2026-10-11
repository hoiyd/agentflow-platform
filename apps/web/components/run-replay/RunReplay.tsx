"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { ChevronDown, GitCompareArrows } from "lucide-react";
import type { RecoveryAction } from "../../lib/api";
import { resumeRun } from "../../lib/api";
import { createLatestRequestController } from "../../lib/latest-request";
import { useReplayData } from "./useReplayData";
import { EpisodeReportPanel } from "./EpisodeReportPanel";
import { RunUsagePanel } from "./RunUsagePanel";
import {
  EventDetail,
  Metric,
  eventDuration,
  formatDuration,
  formatTokenValue,
  stepDuration
} from "./RunEventDetails";
import { RetrievalOverview, buildRetrievalSummary } from "./RunRetrievalDetails";
import { TaskStateChanges } from "./TaskStateChanges";
import { RuntimeDiagnostics } from "./RuntimeDiagnostics";
import { SkillEvidencePanel } from "./SkillEvidence";
import { RecoverySummaryPanel, ToolEffectReconciliationPanel } from "./RecoveryActions";
import { MessageCitations } from "../chat/MarkdownContent";
import { ToolProgressPanel } from "../chat/ToolProgressPanel";

type Props = {
  runId: string;
  initialEventId?: string;
};

export function RunReplay(props: Props) {
  // Changing Run identity resets local commands and selection, not server work.
  return <RunReplayView key={props.runId} {...props} />;
}

function RunReplayView({ runId, initialEventId }: Props) {
  const router = useRouter();
  const { replay, episodeReport, episodeReportError, loadError, refreshError,
    refresh: refreshReplay, invalidateReads, updateRunStatus } = useReplayData(runId);
  const [selection, setSelection] = useState({ initialEventId, id: initialEventId ?? "" });
  const selectedEventId = selection.initialEventId === initialEventId ? selection.id : initialEventId ?? "";
  const setSelectedEventId = (id: string) => setSelection({ initialEventId, id });
  const [operationError, setOperationError] = useState("");
  const [isResuming, setIsResuming] = useState(false);
  const resumeInFlight = useRef(false);
  const [commands] = useState(createLatestRequestController);

  useEffect(() => () => commands.cancel(), [commands]);

  const selectedEvent = useMemo(
    () => replay?.run_events.find((event) => event.id === selectedEventId) ?? replay?.run_events[0],
    [replay, selectedEventId]
  );
  const retrievalSummary = useMemo(() => buildRetrievalSummary(replay?.run_events ?? []), [replay?.run_events]);
  // The Run page always belongs to a Conversation, so "back" returns to the
  // workbench that owns it rather than the landing page. The comparison page
  // carries the same Conversation so its own back link stays equivalent.
  const conversationId = replay?.run.conversation_id ?? "";
  const chatHref = conversationId ? `/workspace?conversation=${encodeURIComponent(conversationId)}` : "/workspace";
  const compareParams = new URLSearchParams();
  if (replay?.run.id) compareParams.set("run", replay.run.id);
  if (conversationId) compareParams.set("conversation", conversationId);
  const compareHref = `/evaluations/compare?${compareParams.toString()}`;
  async function handleResumeRecoverable() {
    if (!replay || replay.run.status !== "failed_recoverable" || resumeInFlight.current) return;
    const request = commands.begin();
    resumeInFlight.current = true;
    setIsResuming(true);
    setOperationError("");
    invalidateReads();
    let navigated = false;
    try {
      await resumeRun({ run_id: replay.run.id, user_input: "Resume failed recoverable run from replay." }, event => {
        if (!request.isCurrent()) return;
        if (event.type !== "run_state" && event.type !== "done") return;
        if (event.run_id && event.run_id !== runId) return;
        if (event.status) updateRunStatus(event.status);
        if (event.type === "run_state" && !navigated) {
          navigated = true;
          router.push(`/workspace?conversation=${encodeURIComponent(event.conversation_id ?? replay.run.conversation_id)}`);
        }
      }, request.signal);
      if (request.isCurrent()) await refreshReplay();
    } catch (error) {
      if (request.isCurrent()) setOperationError(error instanceof Error ? error.message : "Failed to resume run");
    } finally {
      if (request.isCurrent()) {
        resumeInFlight.current = false;
        setIsResuming(false);
      }
    }
  }

  function inspectDiagnosticEvent(eventId: string) {
    if (!replay?.run_events.some((event) => event.id === eventId)) {
      return;
    }
    setSelectedEventId(eventId);
    requestAnimationFrame(() => {
      document.getElementById("run-event-detail")?.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  }

	function handleRecoveryAction(action: RecoveryAction) {
		if (!action.enabled) return;
		switch (action.kind) {
			case "resume_run":
				void handleResumeRecoverable();
				return;
			case "continue_in_chat":
				router.push(`/workspace?conversation=${encodeURIComponent(action.target_id ?? replay?.run.conversation_id ?? "")}`);
				return;
			case "review_verification":
				document.getElementById("run-verification-evidence")?.scrollIntoView({ behavior: "smooth", block: "start" });
				return;
			case "review_task_state":
				document.getElementById("run-task-state")?.scrollIntoView({ behavior: "smooth", block: "start" });
				return;
			case "reconcile_tool_effect":
				document.getElementById("tool-effect-recovery")?.scrollIntoView({ behavior: "smooth", block: "start" });
		}
	}

  if (loadError && !replay) {
    return (
      <main className="replay-page">
        <Link className="back-link" href={chatHref}>
          Back to chat
        </Link>
        <div className="error">{loadError}</div>
      </main>
    );
  }

  if (!replay) {
    return (
      <main className="replay-page">
        <Link className="back-link" href={chatHref}>
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
          <Link className="back-link" href={chatHref}>
            Back to chat
          </Link>
          <h1>Run replay</h1>
          <p>{replay.conversation.title}</p>
        </div>
        <div className="replay-header-actions">
          <span className={`replay-status ${replay.run.status}`}>{replay.run.status}</span>
          <details className="replay-evaluation-menu">
            <summary>
              Evaluation
              <ChevronDown aria-hidden="true" size={13} />
            </summary>
            <div className="replay-evaluation-menu-content">
              <Link href={compareHref}>
                <GitCompareArrows aria-hidden="true" size={15} />
                Compare with another run
              </Link>
            </div>
          </details>
        </div>
      </header>

      {operationError ? <div className="error" role="alert">{operationError}</div> : null}
      {refreshError ? <div className="replay-secondary-error" role="status">Replay refresh unavailable: {refreshError}</div> : null}

		<RecoverySummaryPanel summary={replay.recovery_summary} onAction={handleRecoveryAction} isResuming={isResuming} canResume={replay.run.status === "failed_recoverable"} />
		{replay.recovery_summary?.actions.some((action) => action.kind === "reconcile_tool_effect") ? (
			<ToolEffectReconciliationPanel onChanged={refreshReplay} runId={replay.run.id} />
		) : null}

      <RuntimeDiagnostics
        asOfSequence={replay.projection.as_of_sequence}
        failures={replay.projection.invariant_failures}
        onInspectEvent={inspectDiagnosticEvent}
      />
      <SkillEvidencePanel items={replay.projection.skill_evidence ?? []} onInspectEvent={inspectDiagnosticEvent} />
      <ToolProgressPanel items={replay.projection.tool_progress ?? []} runStatus={replay.run.status} />

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
      {episodeReportError ? (
        <div className="replay-secondary-error" role="status">
          Episode report unavailable: {episodeReportError}
        </div>
      ) : null}

      <TaskStateChanges revisions={replay.task_state_revisions} runId={replay.run.id} />

      <RetrievalOverview summary={retrievalSummary} />

      {replay.messages.map((message) => {
        const sources = message.web_citations?.filter((source) => source.run_id === replay.run.id);
        return sources?.length ? <MessageCitations key={message.id} webCitations={sources} /> : null;
      })}

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

        <div className="detail-panel" id="run-event-detail">
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
