import { useState } from "react";
import type { AgentRoutingRequirements } from "../../lib/api";
import type { AutonomousProgress } from "./AutonomousPanel";
import type { CollaborationStepView } from "./CollaborationPanels";

function emptyRoutingRequirements(): AgentRoutingRequirements {
  return {
    required_tools: [], prohibited_tools: [], require_memory: false,
    require_retrieval: false, preferred_capabilities: []
  };
}

export function useRunTrace() {
  const [collaborationSteps, setCollaborationSteps] = useState<CollaborationStepView[]>([]);
  const [autonomousProgress, setAutonomousProgress] = useState<AutonomousProgress | null>(null);
  const [selectedCollaborationRole, setSelectedCollaborationRole] = useState("planner");
  const [planDraft, setPlanDraft] = useState("");
  const [routingRequirements, setRoutingRequirements] = useState<AgentRoutingRequirements>(emptyRoutingRequirements);

  function reset() {
    setCollaborationSteps([]);
    setAutonomousProgress(null);
    setPlanDraft("");
    setRoutingRequirements(emptyRoutingRequirements());
    setSelectedCollaborationRole("planner");
  }

  function restore(previous: {
    collaborationSteps: CollaborationStepView[]; autonomousProgress: AutonomousProgress | null;
    selectedCollaborationRole: string; planDraft: string;
    routingRequirements: AgentRoutingRequirements;
  }) {
    setCollaborationSteps(previous.collaborationSteps);
    setAutonomousProgress(previous.autonomousProgress);
    setSelectedCollaborationRole(previous.selectedCollaborationRole);
    setPlanDraft(previous.planDraft);
    setRoutingRequirements(previous.routingRequirements);
  }

  return {
    collaborationSteps, setCollaborationSteps, autonomousProgress, setAutonomousProgress,
    selectedCollaborationRole, setSelectedCollaborationRole,
    planDraft, setPlanDraft, routingRequirements, setRoutingRequirements, reset, restore
  };
}
