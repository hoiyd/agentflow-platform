import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";

import type { AgentInfo, AgentRoutingRequirements } from "../../lib/api";
import { CollaborationPanel } from "./CollaborationPanels";

afterEach(cleanup);

it("submits user-approved routing requirements with the plan", () => {
  const onContinue = vi.fn();
  render(<RoutingRequirementsHarness onContinue={onContinue} />);

  fireEvent.click(screen.getByRole("checkbox", { name: "Require calculator" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Knowledge retrieval required" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Preferred capabilities" }), {
    target: { value: "quantitative analysis\nforecasting" }
  });
  fireEvent.click(screen.getByRole("button", { name: "Approve & Continue" }));

  expect(onContinue).toHaveBeenCalledWith("Calculate the forecast.", {
    required_tools: ["calculator"],
    prohibited_tools: [],
    require_memory: false,
    require_retrieval: true,
    preferred_capabilities: ["quantitative analysis", "forecasting"]
  });
});

it("keeps required and prohibited tool choices mutually exclusive", () => {
  render(<RoutingRequirementsHarness onContinue={vi.fn()} />);

  fireEvent.click(screen.getByRole("checkbox", { name: "Require calculator" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Prohibit calculator" }));

  expect((screen.getByRole("checkbox", { name: "Require calculator" }) as HTMLInputElement).checked).toBe(false);
  expect((screen.getByRole("checkbox", { name: "Prohibit calculator" }) as HTMLInputElement).checked).toBe(true);
});

function RoutingRequirementsHarness({ onContinue }: { onContinue: ReturnType<typeof vi.fn> }) {
  const [requirements, setRequirements] = useState<AgentRoutingRequirements>({
    required_tools: [],
    prohibited_tools: [],
    require_memory: false,
    require_retrieval: false,
    preferred_capabilities: []
  });
  return (
    <CollaborationPanel
      agents={[agent]}
      isContinuing={false}
      onCollapse={vi.fn()}
      onContinue={onContinue}
      planDraft="Calculate the forecast."
      routingRequirements={requirements}
      runStatus="waiting_for_user"
      selectedRole="planner"
      setPlanDraft={vi.fn()}
      setRoutingRequirements={setRequirements}
      steps={[{ role: "planner", status: "completed", output: "Calculate the forecast." }]}
    />
  );
}

const agent: AgentInfo = {
  id: "agent-data",
  name: "Data Analyst",
  description: "Analyzes operational data.",
  system_prompt: "Analyze data.",
  routing_hints: { capabilities: ["forecasting"], task_examples: [], exclusions: [] },
  tools: ["calculator"],
  memory_enabled: true,
  retrieval_enabled: true,
  created_at: "2026-09-12T00:00:00Z",
  updated_at: "2026-09-12T00:00:00Z"
};
