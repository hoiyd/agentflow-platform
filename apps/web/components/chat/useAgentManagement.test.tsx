import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { archiveAgent, createAgent, listAgents, listSkills, updateAgent } from "../../lib/api";
import { useAgentManagement } from "./useAgentManagement";

vi.mock("../../lib/api", () => ({
  listAgents: vi.fn(),
  listSkills: vi.fn(),
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

it("keeps frozen profile bindings editable when the skill catalog fails", async () => {
  vi.mocked(listAgents).mockResolvedValue([
    { ...agent("agent_planner", "Planner"), skills: ["knowledge-answer"] },
    { ...agent("template_writer", "Writer"), is_template: true, skills: ["knowledge-answer"] }
  ]);
  vi.mocked(listSkills).mockRejectedValue(new Error("Catalog unavailable"));
  const { result } = renderHook(() => useAgentManagement(false, vi.fn()));
  await act(async () => { await result.current.refreshAgents(); await result.current.refreshSkills(); });
  expect(result.current.skillsError).toBe("Catalog unavailable");
  expect(result.current.agentsError).toBe("");
  expect(result.current.agentConfigDraft?.skills).toEqual(["knowledge-answer"]);
  act(() => result.current.handleOpenNewAgentForm());
  expect(result.current.newAgentDraft?.skills).toEqual([]);
  act(() => result.current.selectNewAgentTemplate("template_writer"));
  expect(result.current.newAgentDraft?.skills).toEqual(["knowledge-answer"]);
});

it("never selects templates, including after archiving the last owned Agent", async () => {
  const template = { ...agent("template_writer", "Writer template"), is_template: true };
  vi.mocked(listAgents).mockResolvedValue([template]);
  const onChange = vi.fn();
  const { result } = renderHook(() => useAgentManagement(false, onChange));
  await act(async () => { await result.current.refreshAgents(); });
  expect(result.current.activeAgentId).toBe("");
  expect(result.current.workspaceAgents).toEqual([]);
  expect(result.current.agentTemplates).toEqual([template]);
  act(() => result.current.selectAgent(template.id));
  expect(result.current.activeAgent).toBeUndefined();
  expect(onChange).not.toHaveBeenCalled();

  vi.mocked(listAgents).mockResolvedValue([template, agent("owned", "Owned")]);
  await act(async () => { await result.current.refreshAgents(); });
  expect(result.current.activeAgentId).toBe("owned");
  act(() => result.current.handleArchiveAgent());
  vi.mocked(archiveAgent).mockResolvedValue(undefined);
  await act(async () => { await result.current.confirmArchiveAgent(); });
  expect(result.current.activeAgentId).toBe("");
  expect(result.current.agentConfigDraft).toBeNull();
  expect(result.current.agentTemplates).toEqual([template]);
});

it("copies the complete template only on request and preserves the draft on creation failure", async () => {
  const template = {
    ...agent("template_writer", "Writer"), is_template: true, description: "Evidence writer",
    system_prompt: "Write with evidence.", tools: ["calculator"], skills: ["writing"],
    routing_hints: { capabilities: ["writing"], task_examples: ["Write a report"], exclusions: ["coding"] },
    memory_enabled: false, retrieval_enabled: false
  };
  vi.mocked(listAgents).mockResolvedValue([template]);
  vi.mocked(createAgent).mockRejectedValueOnce(new Error("Creation denied"));
  const { result } = renderHook(() => useAgentManagement(false, vi.fn()));
  await act(async () => { await result.current.refreshAgents(); });
  act(() => result.current.handleOpenNewAgentForm());
  expect(result.current.newAgentTemplateId).toBe("");
  expect(result.current.newAgentDraft?.tools).toEqual([]);
  act(() => result.current.selectNewAgentTemplate(template.id));
  expect(result.current.newAgentDraft).toEqual({
    name: "Copy of Writer", description: template.description, system_prompt: template.system_prompt,
    tools: template.tools, skills: template.skills, routing_hints: template.routing_hints,
    memory_enabled: false, retrieval_enabled: false
  });
  act(() => result.current.toggleNewAgentTool("calculator"));
  expect(template.tools).toEqual(["calculator"]);
  await act(async () => { await result.current.handleCreateAgent(); });
  expect(result.current.isNewAgentFormOpen).toBe(true);
  expect(result.current.newAgentTemplateId).toBe(template.id);
  expect(result.current.newAgentDraft?.tools).toEqual([]);
  expect(result.current.agentOperationNotice?.message).toBe("Creation denied");
  act(() => result.current.handleCancelNewAgent());
  expect(result.current.newAgentDraft).toBeNull();
  act(() => result.current.handleOpenNewAgentForm());
  expect(result.current.newAgentTemplateId).toBe("");
  expect(result.current.newAgentDraft?.name).toBe("");
  act(() => result.current.selectNewAgentTemplate("missing"));
  expect(result.current.newAgentDraft?.name).toBe("");
  act(() => result.current.selectNewAgentTemplate(template.id));
  act(() => result.current.selectNewAgentTemplate(""));
  expect(result.current.newAgentDraft?.system_prompt).toBe("You are a helpful AgentFlow agent.");
  expect(result.current.newAgentDraft?.skills).toEqual([]);
});

function agent(id: string, name: string): Awaited<ReturnType<typeof listAgents>>[number] {
  return {
    id, name, description: "", system_prompt: "", tools: [],
    memory_enabled: true, retrieval_enabled: true,
    created_at: "2026-01-01T00:00:00Z", updated_at: "2026-01-01T00:00:00Z"
  };
}
