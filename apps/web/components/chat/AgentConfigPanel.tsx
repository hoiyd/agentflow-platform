import { X } from "lucide-react";
import type { ReactNode } from "react";

import type { AgentInfo, ToolInfo, SkillInfo } from "../../lib/api";
import { toolAvailabilityLabel } from "../../lib/tool-availability";
import { AgentSkillBindings } from "./AgentSkillBindings";

export type AgentConfigDraft = {
  name: string;
  description: string;
  system_prompt: string;
  routing_hints: {
    capabilities: string[];
    task_examples: string[];
    exclusions: string[];
  };
  tools: string[];
  skills?: string[];
  skill_policies?: AgentInfo["skill_policies"];
  memory_enabled: boolean;
  retrieval_enabled: boolean;
};

export function isDefaultAgent(agent: AgentInfo) {
  return agent.is_template === true;
}

type AgentConfigPanelProps = {
  actionLabel: string;
  availableTools: ToolInfo[];
  availableSkills?: SkillInfo[];
  skillsError?: string;
  canArchive?: boolean;
  disabled: boolean;
  draft: AgentConfigDraft;
  isArchiving?: boolean;
  isSaving: boolean;
  onArchive?: () => void;
  onCancel?: () => void;
  onChange: (update: Partial<AgentConfigDraft>) => void;
  onSave: () => void;
  onToggleTool: (toolName: string) => void;
  status: string;
  title: string;
  creationSource?: ReactNode;
};

export function AgentConfigPanel({
  actionLabel,
  availableTools,
  availableSkills = [],
  skillsError = "",
  canArchive = false,
  disabled,
  draft,
  isArchiving = false,
  isSaving,
  onArchive,
  onCancel,
  onChange,
  onSave,
  onToggleTool,
  status,
  title,
  creationSource
}: AgentConfigPanelProps) {
  return (
    <section className="agent-config-panel">
      <div className="agent-config-header">
        <strong>{title}</strong>
      </div>
      {creationSource}
      <label>
        <span>Name</span>
        <input
          disabled={disabled}
          value={draft.name}
          onChange={(event) => onChange({ name: event.target.value })}
        />
      </label>
      <label>
        <span>Description</span>
        <textarea
          disabled={disabled}
          value={draft.description}
          onChange={(event) => onChange({ description: event.target.value })}
        />
      </label>
      <label>
        <span>System prompt</span>
        <textarea
          disabled={disabled}
          value={draft.system_prompt}
          onChange={(event) => onChange({ system_prompt: event.target.value })}
        />
      </label>
      <details className="agent-routing-hints">
        <summary>Routing signals</summary>
        <div className="agent-routing-hints-grid">
          <label>
            <span>Capabilities</span>
            <textarea
              disabled={disabled}
              value={draft.routing_hints.capabilities.join("\n")}
              onChange={(event) => onChange({
                routing_hints: { ...draft.routing_hints, capabilities: splitRoutingHints(event.target.value) }
              })}
            />
          </label>
          <label>
            <span>Example tasks</span>
            <textarea
              disabled={disabled}
              value={draft.routing_hints.task_examples.join("\n")}
              onChange={(event) => onChange({
                routing_hints: { ...draft.routing_hints, task_examples: splitRoutingHints(event.target.value) }
              })}
            />
          </label>
          <label>
            <span>Excluded tasks</span>
            <textarea
              disabled={disabled}
              value={draft.routing_hints.exclusions.join("\n")}
              onChange={(event) => onChange({
                routing_hints: { ...draft.routing_hints, exclusions: splitRoutingHints(event.target.value) }
              })}
            />
          </label>
        </div>
      </details>
      <div className="agent-config-switches">
        <label>
          <input
            checked={draft.memory_enabled}
            disabled={disabled}
            onChange={(event) => onChange({ memory_enabled: event.target.checked })}
            type="checkbox"
          />
          <span>Memory retrieval</span>
        </label>
        <label>
          <input
            checked={draft.retrieval_enabled}
            disabled={disabled}
            onChange={(event) => onChange({ retrieval_enabled: event.target.checked })}
            type="checkbox"
          />
          <span>Knowledge retrieval</span>
        </label>
      </div>
      <div className="agent-config-tools">
        <span>Tools</span>
        <div>
          {availableTools.length === 0 ? (
            <small>No tools loaded.</small>
          ) : (
            availableTools.map((tool) => (
              <label key={tool.name}>
                <input
                  checked={draft.tools.includes(tool.name)}
                  disabled={disabled || (!tool.enabled && !draft.tools.includes(tool.name))}
                  title={toolAvailabilityLabel(tool)}
                  onChange={() => onToggleTool(tool.name)}
                  type="checkbox"
                />
                <span>{tool.name}{tool.source?.kind === "mcp" ? <small className="agent-tool-source">MCP · {tool.source.server_id} / {tool.source.remote_name}{toolAvailabilityLabel(tool) ? ` · ${toolAvailabilityLabel(tool)}` : ""}</small> : null}</span>
              </label>
            ))
          )}
        </div>
      </div>
      <AgentSkillBindings draft={draft} skills={availableSkills} tools={availableTools} disabled={disabled} error={skillsError} onChange={onChange} />
      <div className="agent-config-actions">
        {status ? (
          <span
            className={status.includes("Failed") ? "agent-config-status error-text" : "agent-config-status"}
          >
            {status}
          </span>
        ) : null}
        {onArchive && canArchive ? (
          <button
            className="secondary-action danger-action"
            disabled={disabled || isArchiving}
            onClick={onArchive}
            type="button"
          >
            {isArchiving ? "Archiving..." : "Archive Agent"}
          </button>
        ) : null}
        {onCancel ? (
          <button
            aria-label={`Cancel ${title.toLowerCase()}`}
            className="secondary-action agent-config-cancel"
            disabled={disabled}
            onClick={onCancel}
            type="button"
          >
            <X size={15} /> Cancel
          </button>
        ) : null}
        <button
          className="send compact-send"
          disabled={disabled || draft.name.trim().length === 0}
          onClick={onSave}
          type="button"
        >
          {isSaving ? "Saving..." : actionLabel}
        </button>
      </div>
    </section>
  );
}

function splitRoutingHints(value: string): string[] {
  return value.split("\n").map((item) => item.trim()).filter(Boolean);
}
