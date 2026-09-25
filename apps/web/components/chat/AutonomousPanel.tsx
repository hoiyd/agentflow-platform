import { PanelRightClose } from "lucide-react";

import { renderMarkdown } from "./MarkdownContent";
import type { CollaborationRole, CollaborationStepView } from "./CollaborationPanels";

export type AutonomousProgress = {
  iteration: number;
  maxIterations: number;
  elapsedSeconds: number;
  maxRuntimeSeconds: number;
  outputChars: number;
  maxOutputChars: number;
  toolCalls: number;
  maxToolCalls: number;
  stopReason?: string;
};

export const autonomousRoles: CollaborationRole[] = [
  { id: "observe", label: "Observe", empty: "Waiting to observe task state." },
  { id: "plan", label: "Plan", empty: "Waiting to plan the next action." },
  { id: "act", label: "Act", empty: "Waiting to execute the current plan." },
  { id: "review", label: "Review", empty: "Waiting to review the action result." },
  { id: "decide", label: "Decide", empty: "Waiting to decide whether to continue." },
  { id: "human_input", label: "Human Input", empty: "Waiting to see whether user input is needed." },
  { id: "final", label: "Final", empty: "Waiting for final synthesis." }
];

export function toAutonomousProgress(event: {
  iteration?: number;
  max_iterations?: number;
  elapsed_seconds?: number;
  max_runtime_seconds?: number;
  output_chars?: number;
  max_output_chars?: number;
  tool_calls?: number;
  max_tool_calls?: number;
  stop_reason?: string;
}): AutonomousProgress {
  return {
    iteration: event.iteration ?? 0,
    maxIterations: event.max_iterations ?? 0,
    elapsedSeconds: event.elapsed_seconds ?? 0,
    maxRuntimeSeconds: event.max_runtime_seconds ?? 0,
    outputChars: event.output_chars ?? 0,
    maxOutputChars: event.max_output_chars ?? 0,
    toolCalls: event.tool_calls ?? 0,
    maxToolCalls: event.max_tool_calls ?? 0,
    stopReason: event.stop_reason
  };
}

type AutonomousPanelProps = {
  humanInputDraft: string;
  isCanceling: boolean;
  isResuming: boolean;
  onCancel: () => void;
  onCollapse: () => void;
  onHumanInputChange: (value: string) => void;
  onResume: (value?: string) => void;
  progress: AutonomousProgress | null;
  runStatus: string;
  steps: CollaborationStepView[];
};

export function AutonomousPanel({
  humanInputDraft,
  isCanceling,
  isResuming,
  onCancel,
  onCollapse,
  onHumanInputChange,
  onResume,
  progress,
  runStatus,
  steps
}: AutonomousPanelProps) {
  const activeIterations = groupAutonomousSteps(steps);
  const latestIteration = progress?.iteration ?? activeIterations[activeIterations.length - 1]?.iteration ?? 0;
  const completedSteps = steps.filter((step) => step.status === "completed").length;
  const canStop = runStatus === "running" || runStatus === "canceling" || runStatus === "waiting_for_user";
  const humanInputStep = steps.find((step) => step.role === "human_input" && step.status === "running");

  return (
    <aside className="collaboration-panel autonomous-panel" aria-label="Autonomous trace">
      <div className="collaboration-panel-header">
        <div><span>Autonomous</span><strong>Loop Trace</strong></div>
        <div className="collaboration-panel-actions">
          <small className="trace-progress-label">
            Iteration {latestIteration || 0}
            {progress?.maxIterations ? ` / ${progress.maxIterations}` : ""} · {completedSteps} steps complete
          </small>
          {canStop ? (
            <button
              className="trace-stop"
              disabled={isCanceling || runStatus === "canceling"}
              onClick={onCancel}
              type="button"
            >
              {isCanceling || runStatus === "canceling" ? "Stopping..." : "Stop"}
            </button>
          ) : null}
          <button className="trace-panel-toggle trace-collapse" onClick={onCollapse} type="button">
            <PanelRightClose size={14} /> Hide
          </button>
        </div>
      </div>
      <div className="autonomous-limit-strip" aria-label="Run status">
        <TraceMetric label="Status" value={runStatus || "idle"} />
        <TraceMetric
          label="Runtime"
          value={`${formatDuration(progress?.elapsedSeconds ?? 0)}${
            progress?.maxRuntimeSeconds ? ` / ${formatDuration(progress.maxRuntimeSeconds)}` : ""
          }`}
        />
        <TraceMetric
          label="Output"
          value={`${progress?.outputChars ?? 0}${progress?.maxOutputChars ? ` / ${progress.maxOutputChars}` : ""}`}
        />
        <TraceMetric
          label="Tool calls"
          value={`${progress?.toolCalls ?? 0}${progress?.maxToolCalls ? ` / ${progress.maxToolCalls}` : ""}`}
        />
        {progress?.stopReason ? (
          <TraceMetric className="trace-stop-reason" label="Stop reason" value={progress.stopReason} />
        ) : null}
      </div>
      <div className="autonomous-scroll-area">
        {humanInputStep ? (
          <section className="human-input-panel" aria-label="Human input required">
            <div className="human-input-header">
              <div>
                <span>Input required</span>
                <strong>{humanInputStep.output || "Please provide the missing information."}</strong>
              </div>
              <button
                disabled={isResuming || humanInputDraft.trim().length === 0}
                onClick={() => onResume(humanInputDraft)}
                type="button"
              >
                {isResuming ? "Continuing..." : "Submit & Continue"}
              </button>
            </div>
            {humanInputStep.input ? <p>{humanInputStep.input}</p> : null}
            <textarea
              disabled={isResuming}
              onChange={(event) => onHumanInputChange(event.target.value)}
              placeholder="Provide the missing details..."
              value={humanInputDraft}
            />
          </section>
        ) : null}
        <div className="autonomous-iterations">
          {activeIterations.length === 0 ? (
            <div className="autonomous-empty">Waiting for the first autonomous iteration.</div>
          ) : (
            activeIterations.map((group) => (
              <section className="autonomous-iteration" key={group.iteration}>
                <div className="autonomous-iteration-header">
                  <strong>Iteration {group.iteration}</strong>
                  <span>{group.steps.filter((step) => step.status === "completed").length}/{autonomousRoles.length}</span>
                </div>
                {autonomousRoles.map((role) => {
                  const step = group.steps.find((item) => item.role === role.id);
                  return (
                    <article className={`autonomous-step ${step?.status ?? "idle"}`} key={role.id}>
                      <div className="autonomous-step-header">
                        <strong>{role.label}</strong><span>{step?.status ?? "idle"}</span>
                      </div>
                      <div className="collaboration-output">
                        {step?.output ? renderMarkdown(step.output) : <p>{role.empty}</p>}
                      </div>
                      {step?.error ? <div className="error">{step.error}</div> : null}
                    </article>
                  );
                })}
              </section>
            ))
          )}
        </div>
      </div>
    </aside>
  );
}

function TraceMetric({ className = "", label, value }: { className?: string; label: string; value: string }) {
  return (
    <div className={`trace-metric ${className}`.trim()}>
      <span>{label}</span><strong>{value}</strong>
    </div>
  );
}

function groupAutonomousSteps(steps: CollaborationStepView[]) {
  const grouped = new Map<number, CollaborationStepView[]>();
  for (const step of steps) {
    const iteration = step.iteration && step.iteration > 0 ? step.iteration : 1;
    grouped.set(iteration, [...(grouped.get(iteration) ?? []), step]);
  }
  return [...grouped.entries()]
    .sort(([left], [right]) => left - right)
    .map(([iteration, items]) => ({ iteration, steps: items }));
}

function formatDuration(seconds: number) {
  if (!Number.isFinite(seconds) || seconds <= 0) return "0s";
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  if (minutes <= 0) return `${remainingSeconds}s`;
  return `${minutes}m ${remainingSeconds}s`;
}
