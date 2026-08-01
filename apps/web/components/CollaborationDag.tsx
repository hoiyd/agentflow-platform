"use client";

import { useMemo } from "react";
import type { AgentInfo } from "../lib/api";
import type { CollaborationRole, CollaborationStepView } from "./chat/CollaborationPanels";

type CollaborationDagProps = {
  activeRole: string;
  agents: AgentInfo[];
  className?: string;
  onSelectRole: (role: string) => void;
  roles: CollaborationRole[];
  runStatus: string;
  steps: CollaborationStepView[];
};

type DagNode = {
  id: string;
  label: string;
  status: string;
  agentName?: string;
  summary: string;
};

export function CollaborationDag({
  activeRole,
  agents,
  className,
  onSelectRole,
  roles,
  runStatus,
  steps
}: CollaborationDagProps) {
  const agentNames = useMemo(
    () => new Map(agents.map((agent) => [agent.id, agent.name])),
    [agents]
  );
  const nodes = roles.map((role, index): DagNode => {
    const step = steps.find((item) => item.role === role.id);
    const previousStarted = steps.some(
      (item) => roles.findIndex((candidate) => candidate.id === item.role) < index
    );
    const status =
      role.id === "planner" && runStatus === "waiting_for_user"
        ? "waiting_for_user"
        : step?.status ?? (steps.length > 0 && previousStarted ? "queued" : "idle");
    return {
      id: role.id,
      label: role.label,
      status,
      agentName: step?.agent_id ? agentNames.get(step.agent_id) ?? step.agent_id : undefined,
      summary: summarizeNode(role.id, status, step)
    };
  });

  return (
    <section className={`collaboration-dag ${className ?? ""}`.trim()} aria-label="Collaboration DAG">
      <div className="dag-header">
        <span>Execution graph</span>
        <strong>Planner to Router to Worker to Reviewer to Finalizer</strong>
      </div>
      <div className="dag-flow" role="list">
        {nodes.map((node, index) => (
          <div className="dag-item" key={node.id} role="listitem">
            <button
              className={`dag-node ${node.status} ${activeRole === node.id ? "selected" : ""}`}
              onClick={() => onSelectRole(node.id)}
              type="button"
            >
              <span className="dag-node-index">{index + 1}</span>
              <span className="dag-node-copy">
                <strong>{node.label}</strong>
                <small>{node.agentName ?? node.summary}</small>
              </span>
              <span className="dag-node-status">{node.status}</span>
            </button>
            {index < nodes.length - 1 ? <span className="dag-edge" aria-hidden="true" /> : null}
          </div>
        ))}
      </div>
    </section>
  );
}

function summarizeNode(role: string, status: string, step?: CollaborationStepView) {
  if (step?.error) {
    return "Needs attention";
  }
  if (role === "planner" && status === "waiting_for_user") {
    return "Action required";
  }
  if (status === "completed") {
    return "Completed";
  }
  if (status === "running") {
    return "Running";
  }
  if (status === "queued") {
    return "Queued";
  }
  return "Idle";
}
