import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { AutonomousPanel, toAutonomousProgress } from "./AutonomousPanel";

afterEach(cleanup);

it("shows the loop trace and submits required human input", () => {
  const onResume = vi.fn();
  const progress = toAutonomousProgress({ iteration: 2, max_iterations: 4, tool_calls: 1, max_tool_calls: 3 });

  render(
    <AutonomousPanel
      humanInputDraft="Proceed with the report"
      isCanceling={false}
      isResuming={false}
      onCancel={vi.fn()}
      onCollapse={vi.fn()}
      onHumanInputChange={vi.fn()}
      onResume={onResume}
      progress={progress}
      runStatus="waiting_for_user"
      steps={[
        { role: "observe", iteration: 2, status: "completed", output: "Found the source" },
        { role: "human_input", iteration: 2, status: "running", output: "Confirm the next step" }
      ]}
    />
  );

  expect(screen.getByText("Iteration 2")).toBeTruthy();
  expect(screen.getByText("Found the source")).toBeTruthy();
  expect(within(screen.getByRole("region", { name: "Human input required" })).getByText("Confirm the next step")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Submit & Continue" }));
  expect(onResume).toHaveBeenCalledWith("Proceed with the report");
});
