import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ToolProgressPanel } from "./ToolProgressPanel";

describe("Tool progress", () => {
  it("stays absent without updates and never presents counts as execution success", () => {
    const { rerender } = render(<ToolProgressPanel items={[]} runStatus="running" />);
    expect(screen.queryByRole("region", { name: "Tool progress" })).toBeNull();
    const item = { run_id: "r", turn_id: "t", tool_call_id: "c", tool_name: "sandbox_command", phase: "executing", status: "running" as const, sequence: 4, completed: 1, total: 1 };
    rerender(<ToolProgressPanel items={[item]} runStatus="running" />);
    expect(screen.getByText("Running")).toBeTruthy();
    expect(screen.getByText("1 / 1")).toBeTruthy();
    expect(screen.queryByText("Completed")).toBeNull();
    rerender(<ToolProgressPanel items={[item]} runStatus="canceled" />);
    expect(screen.getByText("Incomplete")).toBeTruthy();
    expect(screen.getByText("Saved at event 4")).toBeTruthy();
  });
});
