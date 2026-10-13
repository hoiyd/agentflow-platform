import type { AgentInfo, SkillInfo, ToolInfo } from "../../lib/api";

type SkillAgent = Pick<AgentInfo, "tools" | "skills" | "skill_policies">;

const reasonLabels: Record<string, string> = {
  not_installed: "Not installed", service_disabled: "Disabled by operator",
  workspace_disabled: "Disabled in Workspace", policy_denied: "Policy denied",
  credential_unavailable: "Credential unavailable", credential_scope_unavailable: "Credential unavailable",
  agent_unbound: "Not bound to Agent", not_ready: "Not ready"
};

export function skillReadiness(skill: SkillInfo | undefined, agent: SkillAgent, tools: ToolInfo[] = []) {
  if (!skill) return { ready: false, policy: "explicit_only" as const, dependencies: [], reason: "Unavailable in trusted catalog" };
  const dependencies = (skill.required_tools ?? []).map(name => {
    const declared = skill.tool_dependencies?.find(item => item.name === name);
    const live = tools.find(item => item.name === name);
    let reason = live ? (live.enabled ? "" : live.excluded_reason ?? live.unavailable_reason ?? "not_ready")
      : declared ? (declared.ready ? "" : declared.reason ?? "not_ready") : "not_ready";
    if (!reason && (declared?.agent_binding_required ?? true) && !agent.tools.includes(name)) reason = "agent_unbound";
    return { name, ready: !reason, description: `${name}: ${reason ? reasonLabels[reason] ?? reason : "Ready"}` };
  });
  const policy = skill.invocation_policy === "explicit_only" || agent.skill_policies?.[skill.name] === "explicit_only" ? "explicit_only" as const : "automatic" as const;
  return { ready: dependencies.every(item => item.ready), policy, dependencies, reason: dependencies.filter(item => !item.ready).map(item => item.description).join("; ") };
}
