import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { listAgents, updateAgent } from "../../lib/api";
import { useAgentManagement } from "./useAgentManagement";

vi.mock("../../lib/api", () => ({
  listAgents: vi.fn(),
  createAgent: vi.fn(),
  updateAgent: vi.fn(),
  archiveAgent: vi.fn()
}));

afterEach(() => vi.clearAllMocks());

it("keeps agent selection and failed edits inside the agent workbench", async () => {
  vi.mocked(listAgents).mockResolvedValue([
    agent("agent_planner", "Planner"),
    agent("agent_writer", "Writer")
  ]);
  vi.mocked(updateAgent).mockRejectedValue(new Error("Update denied"));
  const onActiveAgentChange = vi.fn();
  const { result } = renderHook(() => useAgentManagement(false, onActiveAgentChange));

  await act(async () => { await result.current.refreshAgents(); });
  expect(result.current.activeAgentId).toBe("agent_planner");

  act(() => result.current.selectAgent("agent_writer"));
  expect(result.current.activeAgent?.name).toBe("Writer");
  expect(onActiveAgentChange).toHaveBeenCalledOnce();

  await act(async () => { await result.current.handleSaveAgentConfig(); });
  expect(result.current.agentsError).toBe("Update denied");
  expect(result.current.agentOperationNotice?.tone).toBe("error");
});

function agent(id: string, name: string): Awaited<ReturnType<typeof listAgents>>[number] {
  return {
    id, name, description: "", system_prompt: "", tools: [],
    memory_enabled: true, retrieval_enabled: true,
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z"
  };
}
