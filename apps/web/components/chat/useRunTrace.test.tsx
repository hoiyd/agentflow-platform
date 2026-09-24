import { act, renderHook } from "@testing-library/react";
import { expect, it } from "vitest";
import { useRunTrace } from "./useRunTrace";

it("clears trace drafts when a different run is selected", () => {
  const { result } = renderHook(() => useRunTrace());
  act(() => {
    result.current.setPlanDraft("old plan");
    result.current.setHumanInputDraft("old reply");
    result.current.setRoutingRequirements({
      required_tools: ["old_tool"], prohibited_tools: [], require_memory: false,
      require_retrieval: false, preferred_capabilities: []
    });
  });
  act(() => result.current.reset());
  expect(result.current.planDraft).toBe("");
  expect(result.current.humanInputDraft).toBe("");
  expect(result.current.routingRequirements.required_tools).toEqual([]);
});
