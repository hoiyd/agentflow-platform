import {
  AlertCircle,
  CheckCircle2,
  Circle,
  CircleDot,
  FileText,
  PanelRightClose,
  RefreshCw
} from "lucide-react";
import type { ReactNode } from "react";

import type { TaskItemStatus, TaskState } from "../../lib/api";
import { hasTaskStateFacts, taskStateCounts } from "../../lib/task-state";

type TaskStatePanelProps = {
  error: string;
  isLoading: boolean;
  onClose: () => void;
  onRefresh: () => void;
  state: TaskState | null;
};

export function TaskStatePanel({ error, isLoading, onClose, onRefresh, state }: TaskStatePanelProps) {
  const counts = state ? taskStateCounts(state) : null;
  return (
    <aside aria-label="Conversation task state" className="task-state-panel">
      <header className="task-state-panel-header">
        <div>
          <span>Conversation state</span>
          <strong>Task state</strong>
        </div>
        <div className="task-state-panel-actions">
          <code>v{state?.version ?? 0}</code>
          <button aria-label="Refresh task state" disabled={isLoading} onClick={onRefresh} title="Refresh task state" type="button">
            <RefreshCw className={isLoading ? "is-spinning" : ""} size={14} />
          </button>
          <button aria-label="Hide task state" onClick={onClose} title="Hide task state" type="button">
            <PanelRightClose size={15} />
          </button>
        </div>
      </header>

      <div className="task-state-panel-body">
        {error ? <div className="task-state-panel-error"><AlertCircle size={15} /> {error}</div> : null}
        {!state && isLoading ? <div className="task-state-panel-empty">Loading task state...</div> : null}
        {state && !hasTaskStateFacts(state) ? (
          <div className="task-state-panel-empty">
            <strong>No structured task facts</strong>
            <span>Current revision v{state.version} has no active facts.</span>
          </div>
        ) : null}
        {state && hasTaskStateFacts(state) ? (
          <>
            <div className="task-state-summary" aria-label="Task state summary">
              <SummaryValue label="Open" value={counts?.openTasks ?? 0} />
              <SummaryValue label="Completed" value={counts?.completedTasks ?? 0} />
              <SummaryValue label="Blockers" value={counts?.openBlockers ?? 0} tone={counts?.openBlockers ? "danger" : ""} />
            </div>

            {state.goal ? <StateSection title="Goal"><p className="task-state-goal">{state.goal}</p></StateSection> : null}
            {state.tasks.length > 0 ? (
              <StateSection count={state.tasks.length} title="Tasks">
                <div className="task-state-list">
                  {state.tasks.map((task) => (
                    <div className="task-state-task" key={task.id}>
                      <TaskStatusIcon status={task.status} />
                      <div><strong>{task.title}</strong>{task.details ? <p>{task.details}</p> : null}<code>{task.id}</code></div>
                      <span className={`task-state-status ${task.status}`}>{task.status.replaceAll("_", " ")}</span>
                    </div>
                  ))}
                </div>
              </StateSection>
            ) : null}
            {state.blockers.length > 0 ? (
              <StateSection count={state.blockers.length} title="Blockers">
                <div className="task-state-list">
                  {state.blockers.map((blocker) => (
                    <div className="task-state-fact" key={blocker.id}>
                      <AlertCircle size={14} />
                      <div><strong>{blocker.description}</strong><code>{blocker.id}</code></div>
                      <span className={`task-state-status ${blocker.status}`}>{blocker.status}</span>
                    </div>
                  ))}
                </div>
              </StateSection>
            ) : null}
            {state.constraints.length > 0 ? (
              <StateSection count={state.constraints.length} title="Constraints">
                <div className="task-state-list">
                  {state.constraints.map((constraint) => (
                    <div className="task-state-fact" key={constraint.id}>
                      <CircleDot size={14} /><div><strong>{constraint.statement}</strong><code>{constraint.id}</code></div>
                    </div>
                  ))}
                </div>
              </StateSection>
            ) : null}
            {state.decisions.length > 0 ? (
              <StateSection count={state.decisions.length} title="Decisions">
                <div className="task-state-list">
                  {state.decisions.map((decision) => (
                    <div className="task-state-fact" key={decision.id}>
                      <CheckCircle2 size={14} />
                      <div><strong>{decision.statement}</strong>{decision.rationale ? <p>{decision.rationale}</p> : null}<code>{decision.id}</code></div>
                    </div>
                  ))}
                </div>
              </StateSection>
            ) : null}
            {state.artifact_refs.length > 0 ? (
              <StateSection count={state.artifact_refs.length} title="Artifacts">
                <div className="task-state-artifacts">
                  {state.artifact_refs.map((artifact) => <span key={artifact}><FileText size={13} />{artifact}</span>)}
                </div>
              </StateSection>
            ) : null}
          </>
        ) : null}
      </div>
    </aside>
  );
}

function StateSection({ children, count, title }: { children: ReactNode; count?: number; title: string }) {
  return <section className="task-state-section"><div className="task-state-section-title"><span>{title}</span>{count !== undefined ? <code>{count}</code> : null}</div>{children}</section>;
}

function SummaryValue({ label, tone = "", value }: { label: string; tone?: string; value: number }) {
  return <div className={tone}><span>{label}</span><strong>{value}</strong></div>;
}

function TaskStatusIcon({ status }: { status: TaskItemStatus }) {
  if (status === "completed") return <CheckCircle2 className="completed" size={15} />;
  if (status === "in_progress") return <CircleDot className="in-progress" size={15} />;
  return <Circle className={status} size={15} />;
}
