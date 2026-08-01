import type { AgentInfo, ChatMode, ToolInfo } from "../../lib/api";
import type { CompletionVerificationSettings } from "../../lib/verification";
import { CompletionVerificationPanel } from "../CompletionVerificationPanel";
import { AgentConfigPanel, isDefaultAgent, type AgentConfigDraft } from "./AgentConfigPanel";

export type AgentOperationNotice = {
  title: string;
  message: string;
  tone: "success" | "error";
};

type ChatDialogsProps = {
  activeAgent?: AgentInfo;
  agentArchiveCandidate: AgentInfo | null;
  agentConfigDraft: AgentConfigDraft | null;
  agentConfigStatus: string;
  agentOperationNotice: AgentOperationNotice | null;
  archivingAgentId: string;
  chatMode: ChatMode;
  completionVerificationDraft: CompletionVerificationSettings | null;
  completionVerificationError: string;
  isAgentConfigOpen: boolean;
  isCreatingAgent: boolean;
  isNewAgentFormOpen: boolean;
  isSavingAgentConfig: boolean;
  isStreaming: boolean;
  newAgentDraft: AgentConfigDraft | null;
  onArchiveAgent: () => void;
  onCancelAgentConfig: () => void;
  onCancelArchive: () => void;
  onCancelNewAgent: () => void;
  onCompletionVerificationCancel: () => void;
  onCompletionVerificationChange: (update: Partial<CompletionVerificationSettings>) => void;
  onConfirmArchive: () => void;
  onCreateAgent: () => void;
  onDismissNotice: () => void;
  onNewAgentChange: (update: Partial<AgentConfigDraft>) => void;
  onNewAgentToolToggle: (toolName: string) => void;
  onSaveAgentConfig: () => void;
  onSaveCompletionVerification: () => void;
  onUpdateAgentChange: (update: Partial<AgentConfigDraft>) => void;
  onUpdateAgentToolToggle: (toolName: string) => void;
  tools: ToolInfo[];
};

export function ChatDialogs(props: ChatDialogsProps) {
  const {
    activeAgent,
    agentArchiveCandidate,
    agentConfigDraft,
    agentConfigStatus,
    agentOperationNotice,
    archivingAgentId,
    chatMode,
    completionVerificationDraft,
    completionVerificationError,
    isAgentConfigOpen,
    isCreatingAgent,
    isNewAgentFormOpen,
    isSavingAgentConfig,
    isStreaming,
    newAgentDraft,
    onArchiveAgent,
    onCancelAgentConfig,
    onCancelArchive,
    onCancelNewAgent,
    onCompletionVerificationCancel,
    onCompletionVerificationChange,
    onConfirmArchive,
    onCreateAgent,
    onDismissNotice,
    onNewAgentChange,
    onNewAgentToolToggle,
    onSaveAgentConfig,
    onSaveCompletionVerification,
    onUpdateAgentChange,
    onUpdateAgentToolToggle,
    tools
  } = props;

  return (
    <>
      {completionVerificationDraft ? (
        <div className="modal-backdrop agent-config-modal-backdrop" role="presentation">
          <section aria-label="Completion verification" aria-modal="true" className="agent-config-dialog verification-config-dialog" role="dialog">
            <CompletionVerificationPanel
              draft={completionVerificationDraft}
              error={completionVerificationError}
              onCancel={onCompletionVerificationCancel}
              onChange={onCompletionVerificationChange}
              onSave={onSaveCompletionVerification}
            />
          </section>
        </div>
      ) : null}
      {chatMode === "single" && isNewAgentFormOpen && newAgentDraft ? (
        <div className="modal-backdrop agent-config-modal-backdrop create-agent-modal-backdrop" role="presentation">
          <section aria-label="Create new agent" aria-modal="true" className="agent-config-dialog" role="dialog">
            <AgentConfigPanel
              actionLabel="Create Agent"
              availableTools={tools}
              draft={newAgentDraft}
              disabled={isStreaming || isCreatingAgent}
              isSaving={isCreatingAgent}
              onCancel={onCancelNewAgent}
              onChange={onNewAgentChange}
              onSave={onCreateAgent}
              onToggleTool={onNewAgentToolToggle}
              status={agentConfigStatus}
              title="Create new agent"
            />
          </section>
        </div>
      ) : null}
      {chatMode === "single" && isAgentConfigOpen && activeAgent && agentConfigDraft ? (
        <div className="modal-backdrop agent-config-modal-backdrop" role="presentation">
          <section aria-label="Edit agent config" aria-modal="true" className="agent-config-dialog" role="dialog">
            <AgentConfigPanel
              actionLabel="Save Config"
              availableTools={tools}
              canArchive={!isDefaultAgent(activeAgent)}
              draft={agentConfigDraft}
              disabled={isStreaming || isSavingAgentConfig}
              isArchiving={archivingAgentId === activeAgent.id}
              isSaving={isSavingAgentConfig}
              onArchive={onArchiveAgent}
              onCancel={onCancelAgentConfig}
              onChange={onUpdateAgentChange}
              onSave={onSaveAgentConfig}
              onToggleTool={onUpdateAgentToolToggle}
              status={agentConfigStatus}
              title="Edit agent config"
            />
          </section>
        </div>
      ) : null}
      {agentArchiveCandidate ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-labelledby="archive-agent-title" aria-modal="true" className="confirm-dialog" role="dialog">
            <div>
              <span className="dialog-eyebrow">Archive agent</span>
              <h2 id="archive-agent-title">{agentArchiveCandidate.name}</h2>
              <p>This agent will be removed from the active agent list. Existing conversations and replay history will remain available.</p>
            </div>
            <div className="confirm-dialog-actions">
              <button className="secondary-action" disabled={Boolean(archivingAgentId)} onClick={onCancelArchive} type="button">Cancel</button>
              <button className="danger-primary" disabled={Boolean(archivingAgentId)} onClick={onConfirmArchive} type="button">
                {archivingAgentId ? "Archiving..." : "Archive Agent"}
              </button>
            </div>
          </section>
        </div>
      ) : null}
      {agentOperationNotice ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-labelledby="agent-operation-notice-title" aria-modal="true" className={`confirm-dialog operation-notice ${agentOperationNotice.tone}`} role="dialog">
            <div>
              <span className="dialog-eyebrow">{agentOperationNotice.tone === "success" ? "Success" : "Action failed"}</span>
              <h2 id="agent-operation-notice-title">{agentOperationNotice.title}</h2>
              <p>{agentOperationNotice.message}</p>
            </div>
            <div className="confirm-dialog-actions">
              <button className="send compact-send" onClick={onDismissNotice} type="button">OK</button>
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
}
