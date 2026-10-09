import { useId, type FormEvent, type ReactNode } from "react";
import { Info, Send, Settings2, ShieldCheck, UserRoundPlus } from "lucide-react";

import type { AgentInfo, ChatMode } from "../../lib/api";
import { useWorkspaceReadOnly } from "../identity/WorkspaceContext";
import { SelectionMenu } from "../ui/SelectionMenu";

type ChatComposerProps = {
  inbox?: ReactNode;
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
  const needsAgent = chatMode === "single" && !activeAgent;
  const descriptionId = useId();
  const boundSkills = activeAgent?.skills ?? [];

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
          <div className="agent-context">
            <SelectionMenu label="Agent" className="agent-select" value={activeAgentId} onChange={onAgentChange}
              disabled={isStreaming} placement="above"
              options={agents.map(agent => ({value: agent.id, label: agent.name, description: agent.description}))} />
            {activeAgent?.description ? (
              <button aria-expanded={isAgentDescriptionExpanded} aria-controls={descriptionId}
                aria-label={isAgentDescriptionExpanded ? "Hide agent description" : "Show agent description"}
                title={isAgentDescriptionExpanded ? "Hide agent description" : "Show agent description"}
                className="agent-description-toggle" onClick={onDescriptionExpandedChange} type="button">
                <Info size={16} />
              </button>
            ) : null}
            <SelectionMenu label="Skill" className="composer-skill-select" searchable={false} placement="above"
              disabledNote={boundSkills.length ? undefined : activeAgent
                ? "No skills are assigned to this agent. Skills are configured individually for each agent."
                : "No agent is selected. Skill availability depends on the selected agent's configuration."}
              disabled={readOnly || boundSkills.length === 0 || isStreaming || isAwaitingHumanInput || isAwaitingPlanApproval}
              value={boundSkills.length ? input.match(/^\/skill:([^\s]+)/)?.[1] ?? "" : ""}
              options={[
                {value: "", label: boundSkills.length ? "Automatic" : "No skills"},
                ...boundSkills.map(name => ({value: name, label: name}))
              ]}
              onChange={name => {
                const task = input.replace(/^\/skill:[^\s]+\s*/, "");
                onInputChange(name ? `/skill:${name} ${task}` : task);
              }} />
          </div>
          <div className="agent-actions" role="group" aria-label="Agent and run settings">
            {showAgentActions ? (
              <>
                <button
                  className={`agent-create-button ${isNewAgentFormOpen ? "active" : ""}`}
                  disabled={readOnly || isCreatingAgent || isStreaming}
                  onClick={onNewAgent}
                  type="button"
                >
                  <UserRoundPlus size={15} /> New agent
                </button>
                <button className="agent-config-toggle" disabled={readOnly || !activeAgent} onClick={onConfigureAgent} type="button">
                  <Settings2 size={15} /> Configure
                </button>
              </>
            ) : null}
            <div className="agent-run-policy">{verificationButton}</div>
          </div>
          {isAgentDescriptionExpanded && activeAgent?.description ? (
            <div className="agent-description" role="region" aria-label="Agent description" id={descriptionId}>
              {activeAgent.description}
            </div>
          ) : null}
        </div>
      ) : null}
      {chatMode !== "single" ? <div className="composer-run-options">{verificationButton}</div> : null}
      {chatMode === "single" && agentsError ? <div className="error">{agentsError}</div> : null}
      {error ? <div className="error">{error}</div> : null}
      {props.inbox}
      <form className="composer-inner" onSubmit={event => {
        if (readOnly || needsAgent || isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput) event.preventDefault();
        else onSubmit(event);
      }}>
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
            needsAgent ? "Create an agent to start a conversation..." : isAwaitingPlanApproval
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
          disabled={readOnly || needsAgent || isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput || input.trim().length === 0}
        >
          <Send size={18} />
        </button>
      </form>
    </section>
  );
}
