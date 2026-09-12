import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { AgentConfigPanel, type AgentConfigDraft } from "./AgentConfigPanel";

afterEach(cleanup);

it("edits declarative routing hints as normalized line lists", () => {
  const onChange = vi.fn();
  render(
    <AgentConfigPanel
      actionLabel="Save"
      availableTools={[]}
      disabled={false}
      draft={draft}
      isSaving={false}
      onChange={onChange}
      onSave={vi.fn()}
      onToggleTool={vi.fn()}
      status=""
      title="Configure agent"
    />
  );

  fireEvent.change(screen.getByRole("textbox", { name: "Capabilities" }), {
    target: { value: "Go APIs\n\n  React debugging  " }
  });

  expect(onChange).toHaveBeenCalledWith({
    routing_hints: {
      ...draft.routing_hints,
      capabilities: ["Go APIs", "React debugging"]
    }
  });
});

const draft: AgentConfigDraft = {
  name: "Coding Agent",
  description: "Implements software changes.",
  system_prompt: "Write maintainable code.",
  routing_hints: {
    capabilities: ["software implementation"],
    task_examples: ["Fix a Go API"],
    exclusions: ["market research"]
  },
  tools: [],
  memory_enabled: false,
  retrieval_enabled: false
};
