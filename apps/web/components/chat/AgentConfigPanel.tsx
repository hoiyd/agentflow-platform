import { X } from "lucide-react";

import type { AgentInfo, ToolInfo } from "../../lib/api";

export type AgentConfigDraft = {
  name: string;
  description: string;
  system_prompt: string;
  tools: string[];
  memory_enabled: boolean;
  retrieval_enabled: boolean;
};

export function isDefaultAgent(agent: AgentInfo) {
  return ["agent_research", "agent_coding", "agent_data", "agent_planner"].includes(agent.id);
}

type AgentConfigPanelProps = {
  actionLabel: string;
  availableTools: ToolInfo[];
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
};

export function AgentConfigPanel({
  actionLabel,
  availableTools,
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
  title
}: AgentConfigPanelProps) {
  return (
    <section className="agent-config-panel">
      <div className="agent-config-header">
        <strong>{title}</strong>
      </div>
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
                  disabled={disabled}
                  onChange={() => onToggleTool(tool.name)}
                  type="checkbox"
                />
                <span>{tool.name}</span>
              </label>
            ))
          )}
        </div>
      </div>
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
