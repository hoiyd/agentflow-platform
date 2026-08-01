import type { FormEvent } from "react";
import { ChevronDown, ChevronUp, Send, Settings2, ShieldCheck, UserRoundPlus } from "lucide-react";

import type { AgentInfo, ChatMode } from "../../lib/api";

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
          {showAgentActions ? (
            <div className="agent-actions">
              <button
                className={`agent-create-button ${isNewAgentFormOpen ? "active" : ""}`}
                disabled={isCreatingAgent || isStreaming}
                onClick={onNewAgent}
                type="button"
              >
                <UserRoundPlus size={15} /> New agent
              </button>
              <button className="agent-config-toggle" onClick={onConfigureAgent} type="button">
                <Settings2 size={15} /> Configure
              </button>
            </div>
          ) : null}
        </div>
      ) : null}
      <div className="composer-run-options">
        <button
          aria-pressed={completionVerificationEnabled}
          className={`verification-config-toggle ${completionVerificationEnabled ? "active" : ""}`}
          disabled={isStreaming}
          onClick={onOpenVerification}
          type="button"
        >
          <ShieldCheck size={15} /><span>Completion verification</span>
          <strong>{completionVerificationEnabled ? "On" : "Off"}</strong>
        </button>
      </div>
      {chatMode === "single" && agentsError ? <div className="error">{agentsError}</div> : null}
      {error ? <div className="error">{error}</div> : null}
      <form className="composer-inner" onSubmit={onSubmit}>
        <textarea
          disabled={isAwaitingPlanApproval || isAwaitingHumanInput}
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
          disabled={isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput || input.trim().length === 0}
        >
          <Send size={18} />
        </button>
      </form>
    </section>
  );
}
