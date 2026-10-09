import { useMemo, useState } from "react";
import { archiveAgent, createAgent, listAgents, listSkills, updateAgent, type AgentInfo, type SkillInfo } from "../../lib/api";
import { isDefaultAgent, type AgentConfigDraft } from "./AgentConfigPanel";
import type { AgentOperationNotice } from "./ChatDialogs";

export function useAgentManagement(isStreaming: boolean, onActiveAgentChange: () => void) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [activeAgentId, setActiveAgentId] = useState("");
  const [isAgentDescriptionExpanded, setIsAgentDescriptionExpanded] = useState(false);
  const [agentsError, setAgentsError] = useState("");
  const [skills, setSkills] = useState<SkillInfo[]>([]);
  const [skillsError, setSkillsError] = useState("");
  const [isAgentConfigOpen, setIsAgentConfigOpen] = useState(false);
  const [agentConfigDraft, setAgentConfigDraft] = useState<AgentConfigDraft | null>(null);
  const [newAgentDraft, setNewAgentDraft] = useState<AgentConfigDraft | null>(null);
  const [newAgentTemplateId, setNewAgentTemplateId] = useState("");
  const [isNewAgentFormOpen, setIsNewAgentFormOpen] = useState(false);
  const [isSavingAgentConfig, setIsSavingAgentConfig] = useState(false);
  const [isCreatingAgent, setIsCreatingAgent] = useState(false);
  const [archivingAgentId, setArchivingAgentId] = useState("");
  const [agentArchiveCandidate, setAgentArchiveCandidate] = useState<AgentInfo | null>(null);
  const [agentOperationNotice, setAgentOperationNotice] = useState<AgentOperationNotice | null>(null);
  const [agentConfigStatus, setAgentConfigStatus] = useState("");
  // Keep the full catalog for historical Trace names, not runtime selection.
  const workspaceAgents = useMemo(() => agents.filter((item) => !item.is_template), [agents]);
  const agentTemplates = useMemo(() => agents.filter((item) => item.is_template), [agents]);
  const activeAgent = workspaceAgents.find((item) => item.id === activeAgentId);

  async function refreshAgents() {
    try {
      setAgentsError("");
      const items = await listAgents();
      setAgents(items);
      const owned = items.filter((agent) => !agent.is_template);
      const nextAgent = owned.find((agent) => agent.id === activeAgentId) ?? owned[0];
      setActiveAgentId(nextAgent?.id ?? "");
      setAgentConfigDraft(nextAgent ? agentToConfigDraft(nextAgent) : null);
    } catch (err) {
      setAgentsError(err instanceof Error ? err.message : "Failed to load agents");
    }
  }

  async function refreshSkills() {
    try {
      setSkills(await listSkills());
      setSkillsError("");
    } catch (err) {
      setSkillsError(err instanceof Error ? err.message : "Failed to load trusted skills");
    }
  }

  function updateAgentConfigDraft(update: Partial<AgentConfigDraft>) {
    setAgentConfigStatus("");
    setAgentConfigDraft((current) => (current ? { ...current, ...update } : current));
  }

  function updateNewAgentDraft(update: Partial<AgentConfigDraft>) {
    setAgentConfigStatus("");
    setNewAgentDraft((current) => (current ? { ...current, ...update } : current));
  }

  function toggleAgentConfigTool(toolName: string) {
    setAgentConfigStatus("");
    setAgentConfigDraft((current) => {
      if (!current) {
        return current;
      }
      const enabled = current.tools.includes(toolName);
      return {
        ...current,
        tools: enabled ? current.tools.filter((name) => name !== toolName) : [...current.tools, toolName]
      };
    });
  }

  function toggleNewAgentTool(toolName: string) {
    setAgentConfigStatus("");
    setNewAgentDraft((current) => {
      if (!current) {
        return current;
      }
      const enabled = current.tools.includes(toolName);
      return {
        ...current,
        tools: enabled ? current.tools.filter((name) => name !== toolName) : [...current.tools, toolName]
      };
    });
  }

  async function handleSaveAgentConfig() {
    if (!activeAgent || !agentConfigDraft || isSavingAgentConfig) {
      return;
    }
    setIsSavingAgentConfig(true);
    setAgentsError("");
    setAgentConfigStatus("");
    try {
      const updated = await updateAgent(activeAgent.id, agentConfigDraft);
      setAgents((items) => items.map((item) => (item.id === updated.id ? updated : item)));
      setAgentConfigDraft(agentToConfigDraft(updated));
      setAgentConfigStatus("Agent config saved.");
      setIsAgentConfigOpen(false);
      setAgentOperationNotice({
        title: "Agent config saved",
        message: `${updated.name} has been updated and will be used by new runs.`,
        tone: "success"
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to save agent config";
      setAgentsError(message);
      setAgentConfigStatus(message);
      setAgentOperationNotice({
        title: "Failed to save agent",
        message,
        tone: "error"
      });
    } finally {
      setIsSavingAgentConfig(false);
    }
  }

  function handleOpenNewAgentForm() {
    if (isCreatingAgent || isStreaming) {
      return;
    }
    setNewAgentDraft(blankAgentDraft());
    setNewAgentTemplateId("");
    setIsNewAgentFormOpen(true);
    setIsAgentConfigOpen(false);
    setAgentConfigStatus("");
  }

  function selectNewAgentTemplate(templateId: string) {
    if (!newAgentDraft || isCreatingAgent || isStreaming) return;
    const template = agentTemplates.find((agent) => agent.id === templateId);
    if (templateId && !template) return;
    setNewAgentTemplateId(templateId);
    setNewAgentDraft(template ? { ...agentToConfigDraft(template), name: `Copy of ${template.name}` } : blankAgentDraft());
    setAgentConfigStatus("");
  }

  function handleCancelNewAgent() {
    setIsNewAgentFormOpen(false);
    setNewAgentDraft(null);
    setAgentConfigStatus("");
  }

  function handleCancelAgentConfig() {
    if (isSavingAgentConfig) {
      return;
    }
    setIsAgentConfigOpen(false);
    setAgentConfigStatus("");
  }

  async function handleCreateAgent() {
    if (isCreatingAgent || isStreaming || !newAgentDraft) {
      return;
    }
    setIsCreatingAgent(true);
    setAgentsError("");
    setAgentConfigStatus("");
    try {
      const created = await createAgent(newAgentDraft);
      setAgents((items) => [...items, created]);
      setActiveAgentId(created.id);
      setAgentConfigDraft(agentToConfigDraft(created));
      setIsAgentConfigOpen(false);
      setIsNewAgentFormOpen(false);
      setNewAgentDraft(null);
      onActiveAgentChange();
      setAgentConfigStatus("New agent created.");
      setAgentOperationNotice({
        title: "Agent created",
        message: `${created.name} is now available in the active agent list.`,
        tone: "success"
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to create agent";
      setAgentsError(message);
      setAgentConfigStatus(message);
      setAgentOperationNotice({
        title: "Failed to create agent",
        message,
        tone: "error"
      });
    } finally {
      setIsCreatingAgent(false);
    }
  }

  function handleArchiveAgent() {
    if (!activeAgent || isDefaultAgent(activeAgent) || archivingAgentId) {
      return;
    }
    setAgentArchiveCandidate(activeAgent);
  }

  async function confirmArchiveAgent() {
    const candidate = agentArchiveCandidate;
    if (!candidate || isDefaultAgent(candidate) || archivingAgentId) {
      return;
    }
    setArchivingAgentId(candidate.id);
    setAgentsError("");
    setAgentConfigStatus("");
    try {
      await archiveAgent(candidate.id);
      const remaining = agents.filter((agent) => agent.id !== candidate.id);
      setAgents(remaining);
      const next = remaining.find((agent) => !agent.is_template);
      setActiveAgentId(next?.id ?? "");
      setAgentConfigDraft(next ? agentToConfigDraft(next) : null);
      setIsAgentDescriptionExpanded(false);
      setIsAgentConfigOpen(false);
      setIsNewAgentFormOpen(false);
      setNewAgentDraft(null);
      setAgentArchiveCandidate(null);
      onActiveAgentChange();
      setAgentConfigStatus("Agent archived.");
      setAgentOperationNotice({
        title: "Agent archived",
        message: `${candidate.name} was removed from the active agent list. Existing conversations and replay history remain available.`,
        tone: "success"
      });
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to archive agent";
      setAgentsError(message);
      setAgentConfigStatus(message);
      setAgentArchiveCandidate(null);
      setAgentOperationNotice({
        title: "Failed to archive agent",
        message,
        tone: "error"
      });
    } finally {
      setArchivingAgentId("");
    }
  }

  function cancelArchiveAgent() {
    if (!archivingAgentId) {
      setAgentArchiveCandidate(null);
    }
  }

  function closeAgentForms() {
    setIsAgentConfigOpen(false);
    setIsNewAgentFormOpen(false);
    setNewAgentDraft(null);
    setAgentConfigStatus("");
  }

  function selectAgent(agentId: string) {
    const nextAgent = workspaceAgents.find((item) => item.id === agentId);
    if (!nextAgent) return;
    setActiveAgentId(agentId);
    setAgentConfigDraft(nextAgent ? agentToConfigDraft(nextAgent) : null);
    setIsAgentDescriptionExpanded(false);
    onActiveAgentChange();
  }

  function openAgentConfig() {
    if (!activeAgent) return;
    setIsNewAgentFormOpen(false);
    setNewAgentDraft(null);
    setAgentConfigStatus("");
    setIsAgentConfigOpen(true);
  }

  return {
    agents, workspaceAgents, agentTemplates, activeAgent, activeAgentId, isAgentDescriptionExpanded, agentsError, skills, skillsError, refreshSkills,
    isAgentConfigOpen, agentConfigDraft, newAgentDraft, isNewAgentFormOpen,
    isSavingAgentConfig, isCreatingAgent, archivingAgentId, agentArchiveCandidate,
    agentOperationNotice, agentConfigStatus, refreshAgents, closeAgentForms,
    selectAgent, openAgentConfig, newAgentTemplateId, selectNewAgentTemplate,
    toggleDescription: () => setIsAgentDescriptionExpanded((current) => !current),
    dismissNotice: () => setAgentOperationNotice(null),
    updateAgentConfigDraft, updateNewAgentDraft, toggleAgentConfigTool, toggleNewAgentTool,
    handleSaveAgentConfig, handleOpenNewAgentForm, handleCancelNewAgent, handleCancelAgentConfig,
    handleCreateAgent, handleArchiveAgent, confirmArchiveAgent, cancelArchiveAgent
  };
}

function blankAgentDraft(): AgentConfigDraft {
  return {
    name: "", description: "", system_prompt: "You are a helpful AgentFlow agent.",
    routing_hints: { capabilities: [], task_examples: [], exclusions: [] },
    tools: [], skills: [], memory_enabled: true, retrieval_enabled: true
  };
}

function agentToConfigDraft(agent: AgentInfo): AgentConfigDraft {
  return {
    name: agent.name,
    description: agent.description,
    system_prompt: agent.system_prompt,
    routing_hints: {
      capabilities: agent.routing_hints?.capabilities ?? [],
      task_examples: agent.routing_hints?.task_examples ?? [],
      exclusions: agent.routing_hints?.exclusions ?? []
    },
    tools: agent.tools ?? [],
    skills: agent.skills ?? [],
    memory_enabled: agent.memory_enabled ?? true,
    retrieval_enabled: agent.retrieval_enabled ?? true
  };
}
