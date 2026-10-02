import type { FormEvent } from "react";
import { ChevronDown, ChevronUp, Send, Settings2, ShieldCheck, UserRoundPlus } from "lucide-react";

import type { AgentInfo, ChatMode } from "../../lib/api";
import { useWorkspaceReadOnly, useServiceConfigurationReadOnly } from "../identity/WorkspaceContext";

type ChatComposerProps = {
  activeAgent?: AgentInfo;
  activeAgentId: string;
  agents: AgentInfo[];
  agentsError: string;
  chatMode: ChatMode;
  completionVerificationEnabled: boolean;
  error: string;
  input: string;
  isAgentDescriptionExpanded: boolean;
  isAwaitingHumanInput: boolean;
  isAwaitingPlanApproval: boolean;
  isCreatingAgent: boolean;
  isNewAgentFormOpen: boolean;
  isStreaming: boolean;
  onAgentChange: (agentId: string) => void;
  onConfigureAgent: () => void;
  onDescriptionExpandedChange: () => void;
  onInputChange: (value: string) => void;
  onNewAgent: () => void;
  onOpenVerification: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  showAgentActions: boolean;
};

export function ChatComposer(props: ChatComposerProps) {
  const readOnly = useWorkspaceReadOnly();
  const serviceReadOnly = useServiceConfigurationReadOnly();
  const {
    activeAgent,
    activeAgentId,
    agents,
    agentsError,
    chatMode,
    completionVerificationEnabled,
    error,
    input,
    isAgentDescriptionExpanded,
    isAwaitingHumanInput,
    isAwaitingPlanApproval,
    isCreatingAgent,
    isNewAgentFormOpen,
    isStreaming,
    onAgentChange,
    onConfigureAgent,
    onDescriptionExpandedChange,
    onInputChange,
    onNewAgent,
    onOpenVerification,
    onSubmit,
    showAgentActions
  } = props;

  const verificationButton = (
    <button
      aria-pressed={completionVerificationEnabled}
      className={`verification-config-toggle ${completionVerificationEnabled ? "active" : ""}`}
      disabled={isStreaming}
      onClick={onOpenVerification}
      type="button"
    >
      <ShieldCheck size={15} /><span>Verification</span>
      <strong>{completionVerificationEnabled ? "On" : "Off"}</strong>
    </button>
  );

  return (
    <section className="composer">
      {chatMode === "single" ? (
        <div className="agent-bar single">
          <label className="agent-select">
            <span>Agent</span>
            <select
              title={activeAgent?.name ?? "Select an agent"}
              value={activeAgentId}
              disabled={isStreaming || agents.length === 0}
              onChange={(event) => onAgentChange(event.target.value)}
            >
              {agents.map((agent) => <option key={agent.id} title={agent.name} value={agent.id}>{agent.name}</option>)}
            </select>
          </label>
          <div className="agent-summary">
            <strong>{activeAgent?.name ?? "No agent loaded"}</strong>
            <div className={`agent-description ${isAgentDescriptionExpanded ? "expanded" : ""}`}>
              <span>{activeAgent?.description ?? agentsError}</span>
              {activeAgent?.description ? (
                <button
                  aria-expanded={isAgentDescriptionExpanded}
                  aria-label={isAgentDescriptionExpanded ? "Collapse agent description" : "Expand agent description"}
                  className="agent-description-toggle"
                  onClick={onDescriptionExpandedChange}
                  type="button"
                >
                  {isAgentDescriptionExpanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                </button>
              ) : null}
            </div>
          </div>
          <div className="agent-actions">
            {activeAgent?.skills?.length ? (
              <select aria-label="Invoke skill" className="composer-skill-select" title="Invoke a bound skill"
                disabled={isStreaming || isAwaitingHumanInput || isAwaitingPlanApproval}
                value={input.match(/^\/skill:([^\s]+)/)?.[1] ?? ""}
                onChange={(event) => {
                  const task = input.replace(/^\/skill:[^\s]+\s*/, "");
                  onInputChange(event.target.value ? `/skill:${event.target.value} ${task}` : task);
                }}>
                <option value="">Skill: automatic</option>
                {activeAgent.skills.map((name) => <option key={name} value={name}>{name}</option>)}
              </select>
            ) : null}
            {showAgentActions && !serviceReadOnly ? (
              <>
                <button
                  className={`agent-create-button ${isNewAgentFormOpen ? "active" : ""}`}
                  disabled={readOnly || isCreatingAgent || isStreaming}
                  onClick={onNewAgent}
                  type="button"
                >
                  <UserRoundPlus size={15} /> New agent
                </button>
                <button className="agent-config-toggle" disabled={readOnly} onClick={onConfigureAgent} type="button">
                  <Settings2 size={15} /> Configure
                </button>
              </>
            ) : null}
            {verificationButton}
          </div>
        </div>
      ) : null}
      {chatMode !== "single" ? <div className="composer-run-options">{verificationButton}</div> : null}
      {chatMode === "single" && agentsError ? <div className="error">{agentsError}</div> : null}
      {error ? <div className="error">{error}</div> : null}
      <form className="composer-inner" onSubmit={event => { if (readOnly) event.preventDefault(); else onSubmit(event); }}>
        <textarea
          disabled={readOnly || isAwaitingPlanApproval || isAwaitingHumanInput}
          value={input}
          onChange={(event) => onInputChange(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Enter" && !event.shiftKey) {
              event.preventDefault();
              event.currentTarget.form?.requestSubmit();
            }
          }}
          placeholder={
            isAwaitingPlanApproval
              ? "Review and edit the plan in Collaboration Trace, then continue."
              : isAwaitingHumanInput
                ? "Answer the question in Autonomous Trace, then continue."
                : "Ask AgentFlow anything..."
          }
        />
        <button
          aria-label="Send message"
          title="Send message"
          className="send"
          disabled={readOnly || isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput || input.trim().length === 0}
        >
          <Send size={18} />
        </button>
      </form>
    </section>
  );
}
