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
  const [humanInputDraft, setHumanInputDraft] = useState("");
  const [selectedCollaborationRole, setSelectedCollaborationRole] = useState("planner");
  const [planDraft, setPlanDraft] = useState("");
  const [routingRequirements, setRoutingRequirements] = useState<AgentRoutingRequirements>(emptyRoutingRequirements);

  function reset() {
    setCollaborationSteps([]);
    setAutonomousProgress(null);
    setPlanDraft("");
    setRoutingRequirements(emptyRoutingRequirements());
    setHumanInputDraft("");
    setSelectedCollaborationRole("planner");
  }

  return {
    collaborationSteps, setCollaborationSteps, autonomousProgress, setAutonomousProgress,
    humanInputDraft, setHumanInputDraft, selectedCollaborationRole, setSelectedCollaborationRole,
    planDraft, setPlanDraft, routingRequirements, setRoutingRequirements, reset
  };
}
