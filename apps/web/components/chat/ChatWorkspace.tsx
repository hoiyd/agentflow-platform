import type { RefObject } from "react";
import { GitBranch, PanelRightOpen } from "lucide-react";

import type { AgentInfo, ChatMode, Message, TaskState } from "../../lib/api";
import { CollaborationDag } from "../CollaborationDag";
import {
  AutonomousPanel,
  CollaborationPanel,
  collaborationRoles,
  type AutonomousProgress,
  type CollaborationStepView
} from "./CollaborationPanels";
import { MessageCitations, renderMarkdown } from "./MarkdownContent";
import { ModeChooser } from "./ModeChooser";
import { TaskStatePanel } from "./TaskStatePanel";

type ChatWorkspaceProps = {
  agents: AgentInfo[];
  autonomousProgress: AutonomousProgress | null;
  chatMode: ChatMode;
  collaborationSteps: CollaborationStepView[];
  humanInputDraft: string;
  isCanceling: boolean;
  isCollaborationPanelOpen: boolean;
  isContinuing: boolean;
  isResuming: boolean;
  isStreaming: boolean;
  isTaskStatePanelOpen: boolean;
  messages: Message[];
  messagesRef: RefObject<HTMLElement | null>;
  onCancel: () => void;
  onContinue: (plan?: string) => void;
  onHumanInputChange: (value: string) => void;
  onModeChange: (mode: ChatMode) => void;
  onPanelOpenChange: (open: boolean) => void;
  onPromptSelect: (prompt: string) => void;
  onResume: (value?: string) => void;
  onRoleSelect: (role: string) => void;
  onTaskStateClose: () => void;
  onTaskStateRefresh: () => void;
  onPlanDraftChange: (value: string) => void;
  planDraft: string;
  runStatus: string;
  selectedRole: string;
  showAutonomousTrace: boolean;
  showCollaborationDag: boolean;
  showCollaborationPanel: boolean;
  taskState: TaskState | null;
  taskStateError: string;
  taskStateLoading: boolean;
  useExpandedConversationWidth: boolean;
};

export function ChatWorkspace(props: ChatWorkspaceProps) {
  const {
    agents,
    autonomousProgress,
    chatMode,
    collaborationSteps,
    humanInputDraft,
    isCanceling,
    isCollaborationPanelOpen,
    isContinuing,
    isResuming,
    isStreaming,
    isTaskStatePanelOpen,
    messages,
    messagesRef,
    onCancel,
    onContinue,
    onHumanInputChange,
    onModeChange,
    onPanelOpenChange,
    onPlanDraftChange,
    onPromptSelect,
    onResume,
    onRoleSelect,
    onTaskStateClose,
    onTaskStateRefresh,
    planDraft,
    runStatus,
    selectedRole,
    showAutonomousTrace,
    showCollaborationDag,
    showCollaborationPanel,
    taskState,
    taskStateError,
    taskStateLoading,
    useExpandedConversationWidth
  } = props;
  const awaitingPlanApproval = chatMode === "multi_agent" && runStatus === "waiting_for_user";

  return (
    <section
      className={`chat-workspace ${useExpandedConversationWidth ? "expanded-content" : ""} ${
        showCollaborationPanel && isCollaborationPanelOpen ? "with-collaboration" : ""
      } ${isTaskStatePanelOpen ? "with-task-state" : ""} ${showCollaborationDag && isCollaborationPanelOpen ? "with-collaboration-dag" : ""}`}
    >
      {showCollaborationDag && isCollaborationPanelOpen ? (
        <CollaborationDag
          activeRole={selectedRole}
          agents={agents}
          className="collaboration-dag-standalone"
          onSelectRole={onRoleSelect}
          roles={collaborationRoles}
          runStatus={runStatus}
          steps={collaborationSteps}
        />
      ) : null}
      <div className="conversation-column">
        <ModeChooser chatMode={chatMode} disabled={isStreaming} setChatMode={onModeChange} />
        <section className="messages" ref={messagesRef}>
          {showCollaborationPanel && !isCollaborationPanelOpen ? (
            <div className="trace-reveal-row">
              <button
                className="collaboration-rail-toggle trace-panel-toggle"
                onClick={() => onPanelOpenChange(true)}
                type="button"
              >
                <PanelRightOpen size={14} />
                {awaitingPlanApproval
                  ? "Review Plan & Continue"
                  : showAutonomousTrace
                    ? "Show Autonomous Trace"
                    : "Show Collaboration Trace"}
              </button>
            </div>
          ) : null}
          {messages.length === 0 ? (
            <EmptyConversation onPromptSelect={onPromptSelect} />
          ) : (
            messages.map((message) => (
              <article className={`message ${message.role}`} key={message.id}>
                <div className="message-meta">{message.role}</div>
                <div className="bubble">
                  {message.content ? renderMarkdown(message.content) : "..."}
                  <MessageCitations citations={message.citations} />
                </div>
              </article>
            ))
          )}
        </section>
      </div>
      {showCollaborationPanel && isCollaborationPanelOpen ? (
        showAutonomousTrace ? (
          <AutonomousPanel
            humanInputDraft={humanInputDraft}
            isCanceling={isCanceling}
            isResuming={isResuming}
            onCancel={onCancel}
            onCollapse={() => onPanelOpenChange(false)}
            onHumanInputChange={onHumanInputChange}
            onResume={onResume}
            progress={autonomousProgress}
            runStatus={runStatus}
            steps={collaborationSteps}
          />
        ) : (
          <CollaborationPanel
            agents={agents}
            isContinuing={isContinuing}
            onCollapse={() => onPanelOpenChange(false)}
            onContinue={onContinue}
            planDraft={planDraft}
            runStatus={runStatus}
            selectedRole={selectedRole}
            setPlanDraft={onPlanDraftChange}
            steps={collaborationSteps}
          />
        )
      ) : null}
      {isTaskStatePanelOpen ? (
        <TaskStatePanel
          error={taskStateError}
          isLoading={taskStateLoading}
          onClose={onTaskStateClose}
          onRefresh={onTaskStateRefresh}
          state={taskState}
        />
      ) : null}
    </section>
  );
}

function EmptyConversation({ onPromptSelect }: { onPromptSelect: (prompt: string) => void }) {
  return (
    <div className="empty">
      <div className="empty-mark"><GitBranch size={22} strokeWidth={1.5} /></div>
      <span className="empty-eyebrow">Ready to run</span>
      <h2>What should the agents work on?</h2>
      <p>Describe an outcome. Choose direct chat for quick work, collaboration for a reviewed plan, or autonomous mode for bounded execution.</p>
      <div className="starter-prompts" aria-label="Starter prompts">
        <button onClick={() => onPromptSelect("Compare two implementation approaches and recommend one.")} type="button">Compare approaches</button>
        <button onClick={() => onPromptSelect("Research this topic, cite evidence, and summarize the result.")} type="button">Run research</button>
        <button onClick={() => onPromptSelect("Create an execution plan and wait for my approval.")} type="button">Draft a plan</button>
      </div>
    </div>
  );
}
