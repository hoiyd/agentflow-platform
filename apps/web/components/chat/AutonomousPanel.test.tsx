import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { AutonomousPanel, toAutonomousProgress } from "./AutonomousPanel";

afterEach(cleanup);

it("keeps historical human input readable without offering legacy submission", () => {
  const progress = toAutonomousProgress({ iteration: 2, max_iterations: 4, tool_calls: 1, max_tool_calls: 3 });

  render(
    <AutonomousPanel
      isCanceling={false}
      onCancel={vi.fn()}
      onCollapse={vi.fn()}
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
  expect(screen.getAllByText("Confirm the next step").length).toBeGreaterThan(0);
  expect(screen.queryByRole("region", { name: "Human input required" })).toBeNull();
  expect(screen.queryByRole("button", { name: "Submit & Continue" })).toBeNull();
  expect(screen.queryByRole("textbox")).toBeNull();
});
