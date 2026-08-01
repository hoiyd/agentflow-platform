import { useMemo, useRef } from "react";
import { lexer } from "marked";
import { PanelRightClose } from "lucide-react";

import type { AgentInfo } from "../../lib/api";
import { renderMarkdown, renderMarkdownTokens } from "./MarkdownContent";

export type CollaborationStepView = {
  role: string;
  agent_id?: string;
  status: string;
  iteration?: number;
  input?: string;
  output?: string;
  error?: string;
};

export type CollaborationRole = {
  id: string;
  label: string;
  empty: string;
};

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

export const collaborationRoles: CollaborationRole[] = [
  { id: "planner", label: "Planner", empty: "No plan has been generated yet." },
  { id: "router", label: "Router", empty: "Waiting to choose the best worker agent." },
  { id: "worker", label: "Worker", empty: "Waiting for the plan before execution." },
  { id: "reviewer", label: "Reviewer", empty: "Waiting for worker output to review." },
  { id: "finalizer", label: "Finalizer", empty: "Waiting to synthesize the final answer." }
];

export const autonomousRoles: CollaborationRole[] = [
  { id: "observe", label: "Observe", empty: "Waiting to observe task state." },
  { id: "plan", label: "Plan", empty: "Waiting to plan the next action." },
  { id: "act", label: "Act", empty: "Waiting to execute the current plan." },
  { id: "review", label: "Review", empty: "Waiting to review the action result." },
  { id: "decide", label: "Decide", empty: "Waiting to decide whether to continue." },
  { id: "human_input", label: "Human Input", empty: "Waiting to see whether user input is needed." },
  { id: "final", label: "Final", empty: "Waiting for final synthesis." }
];

export function toCollaborationStepView(step: CollaborationStepView) {
  return {
    role: step.role,
    agent_id: step.agent_id,
    status: step.status,
    iteration: step.iteration,
    input: step.input,
    output: step.output,
    error: step.error
  };
}

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

export function upsertCollaborationStep(items: CollaborationStepView[], event: CollaborationStepView) {
  const next = {
    role: event.role,
    agent_id: event.agent_id,
    status: event.status,
    iteration: event.iteration,
    input: event.input,
    output: event.output,
    error: event.error
  };
  const existing = items.findIndex(
    (item) => item.role === event.role && (item.iteration ?? 0) === (event.iteration ?? 0)
  );
  if (existing === -1) {
    return [...items, next];
  }
  return items.map((item, index) => (index === existing ? { ...item, ...next } : item));
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

type CollaborationPanelProps = {
  agents: AgentInfo[];
  isContinuing: boolean;
  onCollapse: () => void;
  onContinue: (plan?: string) => void;
  planDraft: string;
  runStatus: string;
  selectedRole: string;
  setPlanDraft: (value: string) => void;
  steps: CollaborationStepView[];
};

export function CollaborationPanel({
  agents,
  isContinuing,
  onCollapse,
  onContinue,
  planDraft,
  runStatus,
  selectedRole,
  setPlanDraft,
  steps
}: CollaborationPanelProps) {
  const hasStarted = steps.length > 0;
  const isAwaitingPlanApproval = runStatus === "waiting_for_user";
  const plannerStep = steps.find((step) => step.role === "planner");
  const planEditorRef = useRef<HTMLDivElement | null>(null);
  const agentNames = useMemo(() => new Map(agents.map((agent) => [agent.id, agent.name])), [agents]);
  const visibleSteps = collaborationRoles.map((role, index) => {
    const existing = steps.find((step) => step.role === role.id);
    if (existing) return existing;
    const previousStarted = steps.some(
      (step) => collaborationRoles.findIndex((item) => item.id === step.role) < index
    );
    return { role: role.id, status: hasStarted && previousStarted ? "queued" : "idle" };
  });

  return (
    <aside className="collaboration-panel" aria-label="Multi-agent collaboration">
      <div className="collaboration-panel-header">
        <div><span>Multi-Agent</span><strong>Collaboration Trace</strong></div>
        <div className="collaboration-panel-actions">
          <small className="trace-progress-label">
            {visibleSteps.filter((step) => step.status === "completed").length}/{collaborationRoles.length} complete
          </small>
          <button className="trace-panel-toggle trace-collapse" onClick={onCollapse} type="button">
            <PanelRightClose size={14} /> Hide
          </button>
        </div>
      </div>
      <div className="collaboration-scroll-area">
        {isAwaitingPlanApproval ? (
          <section className="plan-review" aria-label="Review generated plan">
            <div className="plan-review-header">
              <div><span>Action required</span><strong>Review the plan before execution</strong></div>
              <button
                disabled={isContinuing || planDraft.trim().length === 0}
                onClick={() => onContinue(planEditorRef.current?.innerText ?? planDraft)}
                type="button"
              >
                {isContinuing ? "Continuing..." : "Approve & Continue"}
              </button>
            </div>
            <div
              aria-label="Editable generated plan"
              className="plan-rich-editor markdown"
              contentEditable={!isContinuing}
              onBlur={(event) => setPlanDraft(event.currentTarget.innerText)}
              ref={planEditorRef}
              role="textbox"
              suppressContentEditableWarning
              tabIndex={0}
            >
              {renderMarkdownTokens(lexer(planDraft))}
            </div>
            <p>Edit the rendered plan directly. The bottom chat input is paused until you continue this run.</p>
          </section>
        ) : null}
        <div className="collaboration-steps">
          {visibleSteps.map((step) => {
            const role = collaborationRoles.find((item) => item.id === step.role);
            const isPlannerWaiting = step.role === "planner" && isAwaitingPlanApproval;
            return (
              <article
                className={`collaboration-step ${step.status} ${selectedRole === step.role ? "selected" : ""}`}
                key={step.role}
              >
                <div className="collaboration-step-header">
                  <div>
                    <strong>{role?.label ?? step.role}</strong>
                    {step.agent_id ? (
                      <span>{agentNames.get(step.agent_id) ?? "Selected agent"} ({step.agent_id})</span>
                    ) : null}
                  </div>
                  <span className="step-status">{step.status}</span>
                </div>
                <div className="collaboration-output">
                  {isPlannerWaiting ? (
                    plannerStep?.output ? renderMarkdown(plannerStep.output) : <p>Plan is ready for review above.</p>
                  ) : step.output ? (
                    renderMarkdown(step.output)
                  ) : (
                    <p>{role?.empty ?? "Waiting for execution."}</p>
                  )}
                </div>
                {step.error ? <div className="error">{step.error}</div> : null}
              </article>
            );
          })}
        </div>
      </div>
    </aside>
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
