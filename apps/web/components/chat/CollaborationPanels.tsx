import { useMemo, useRef } from "react";
import { lexer } from "marked";
import { PanelRightClose } from "lucide-react";

import type { AgentInfo, AgentRoutingRequirements } from "../../lib/api";
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

export const collaborationRoles: CollaborationRole[] = [
  { id: "planner", label: "Planner", empty: "No plan has been generated yet." },
  { id: "router", label: "Router", empty: "Waiting to choose the best worker agent." },
  { id: "worker", label: "Worker", empty: "Waiting for the plan before execution." },
  { id: "reviewer", label: "Reviewer", empty: "Waiting for worker output to review." },
  { id: "finalizer", label: "Finalizer", empty: "Waiting to synthesize the final answer." }
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

type CollaborationPanelProps = {
  agents: AgentInfo[];
  isContinuing: boolean;
  onCollapse: () => void;
  onContinue: (plan?: string, requirements?: AgentRoutingRequirements) => void;
  planDraft: string;
  routingRequirements: AgentRoutingRequirements;
  runStatus: string;
  selectedRole: string;
  setPlanDraft: (value: string) => void;
  setRoutingRequirements: (requirements: AgentRoutingRequirements) => void;
  steps: CollaborationStepView[];
};

export function CollaborationPanel({
  agents,
  isContinuing,
  onCollapse,
  onContinue,
  planDraft,
  routingRequirements,
  runStatus,
  selectedRole,
  setPlanDraft,
  setRoutingRequirements,
  steps
}: CollaborationPanelProps) {
  const hasStarted = steps.length > 0;
  const isAwaitingPlanApproval = runStatus === "waiting_for_user";
  const plannerStep = steps.find((step) => step.role === "planner");
  const planEditorRef = useRef<HTMLDivElement | null>(null);
  const agentNames = useMemo(() => new Map(agents.map((agent) => [agent.id, agent.name])), [agents]);
  const availableTools = useMemo(
    () => Array.from(new Set(agents.flatMap((agent) => agent.tools ?? []))).sort(),
    [agents]
  );
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
                onClick={() => onContinue(planEditorRef.current?.innerText ?? planDraft, routingRequirements)}
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
            <details className="plan-routing-requirements">
              <summary>Routing requirements</summary>
              <div className="routing-requirement-flags">
                <label>
                  <input
                    checked={routingRequirements.require_memory ?? false}
                    disabled={isContinuing}
                    onChange={(event) => setRoutingRequirements({ ...routingRequirements, require_memory: event.target.checked })}
                    type="checkbox"
                  />
                  Memory required
                </label>
                <label>
                  <input
                    checked={routingRequirements.require_retrieval ?? false}
                    disabled={isContinuing}
                    onChange={(event) => setRoutingRequirements({ ...routingRequirements, require_retrieval: event.target.checked })}
                    type="checkbox"
                  />
                  Knowledge retrieval required
                </label>
              </div>
              {availableTools.length > 0 ? (
                <div className="routing-tool-matrix">
                  <span>Tool</span><span>Required</span><span>Prohibited</span>
                  {availableTools.map((tool) => (
                    <RoutingToolRequirement
                      disabled={isContinuing}
                      key={tool}
                      onChange={setRoutingRequirements}
                      requirements={routingRequirements}
                      tool={tool}
                    />
                  ))}
                </div>
              ) : null}
              <label className="routing-preferences">
                <span>Preferred capabilities</span>
                <textarea
                  disabled={isContinuing}
                  onChange={(event) => setRoutingRequirements({
                    ...routingRequirements,
                    preferred_capabilities: splitRoutingList(event.target.value)
                  })}
                  placeholder="research synthesis"
                  value={(routingRequirements.preferred_capabilities ?? []).join("\n")}
                />
              </label>
            </details>
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

function RoutingToolRequirement({
  disabled,
  onChange,
  requirements,
  tool
}: {
  disabled: boolean;
  onChange: (requirements: AgentRoutingRequirements) => void;
  requirements: AgentRoutingRequirements;
  tool: string;
}) {
  const required = requirements.required_tools ?? [];
  const prohibited = requirements.prohibited_tools ?? [];
  return (
    <>
      <strong>{tool}</strong>
      <input
        aria-label={`Require ${tool}`}
        checked={required.includes(tool)}
        disabled={disabled}
        onChange={(event) => onChange({
          ...requirements,
          required_tools: toggleListValue(required, tool, event.target.checked),
          prohibited_tools: prohibited.filter((item) => item !== tool)
        })}
        type="checkbox"
      />
      <input
        aria-label={`Prohibit ${tool}`}
        checked={prohibited.includes(tool)}
        disabled={disabled}
        onChange={(event) => onChange({
          ...requirements,
          prohibited_tools: toggleListValue(prohibited, tool, event.target.checked),
          required_tools: required.filter((item) => item !== tool)
        })}
        type="checkbox"
      />
    </>
  );
}

function toggleListValue(items: string[], value: string, enabled: boolean) {
  return enabled ? [...items, value] : items.filter((item) => item !== value);
}

function splitRoutingList(value: string) {
  return value.split("\n").map((item) => item.trim()).filter(Boolean);
}
