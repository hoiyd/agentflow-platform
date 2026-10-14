import type { ToolInfo, SkillInfo } from "../../lib/api";
import { SelectionMenu } from "../ui/SelectionMenu";
import type { AgentConfigDraft } from "./AgentConfigPanel";
import { skillReadiness } from "./skillReadiness";

export function AgentSkillBindings({ draft, skills, tools, disabled, error, onChange }: {
  draft: AgentConfigDraft; skills: SkillInfo[]; tools: ToolInfo[]; disabled: boolean; error: string;
  onChange: (update: Partial<AgentConfigDraft>) => void;
}) {
  const selected = draft.skills ?? [];
  return <div className="agent-skill-bindings">
    <span className="selection-label">Skills</span>
    {error ? <small className="error-text" role="alert">{error}</small> : null}
    {!skills.length && !selected.length ? <small>No trusted skills configured.</small> : null}
    {[...new Set([...skills.map(item => item.name), ...selected])].map(name => {
      const item = skills.find(item => item.name === name);
      const status = skillReadiness(item, draft, tools);
      const bound = selected.includes(name);
      return <div className="agent-skill-row" key={name}>
        <div className="agent-skill-copy">
          <label>
            <input checked={bound} disabled={disabled} type="checkbox" onChange={() => {
              const policies = { ...draft.skill_policies };
              delete policies[name];
              onChange({ skills: bound ? selected.filter(value => value !== name) : [...selected, name], ...(bound && draft.skill_policies?.[name] ? { skill_policies: policies } : {}) });
            }} />
            <span>{name}{item ? "" : " (unavailable)"}</span>
          </label>
          {item ? <small>{item.description}</small> : null}
          {status.dependencies.map(dependency => <small key={dependency.name} className={dependency.ready ? "" : "error-text"}>{dependency.description}</small>)}
          {!item ? <small className="error-text">{status.reason}</small> : null}
        </div>
        <SelectionMenu label={`Invocation for ${name}`} searchable={false} className="skill-policy-select"
          value={status.policy} disabled={disabled || !bound || !item || item.invocation_policy === "explicit_only"}
          disabledNote={item?.invocation_policy === "explicit_only" ? "The trusted package requires explicit invocation." : undefined}
          options={[{ value: "automatic", label: "Automatic" }, { value: "explicit_only", label: "Explicit only" }]}
          onChange={value => {
            const policies = { ...draft.skill_policies };
            if (value === "automatic") delete policies[name]; else policies[name] = "explicit_only";
            onChange({ skill_policies: policies });
          }} />
      </div>;
    })}
  </div>;
}
