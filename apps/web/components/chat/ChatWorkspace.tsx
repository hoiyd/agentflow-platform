import { Fragment, type RefObject } from "react";
import { GitBranch, LoaderCircle, PanelRightOpen } from "lucide-react";

import type { AgentInfo, AgentRoutingRequirements, ChatMode, Message, TaskState } from "../../lib/api";
import { AutonomousPanel, type AutonomousProgress } from "./AutonomousPanel";
import { CollaborationDag } from "./CollaborationDag";
import {
  CollaborationPanel,
  collaborationRoles,
  type CollaborationStepView
} from "./CollaborationPanels";
import { MessageCitations, renderMarkdown } from "./MarkdownContent";
import { ModeChooser } from "./ModeChooser";
import { TaskStatePanel } from "./TaskStatePanel";
import { ReasoningDisclosure, type ReasoningEntry } from "./ReasoningDisclosure";
import { PartialOutputPanel } from "./PartialOutputPanel";
import { ToolProgressPanel } from "./ToolProgressPanel";
import type { PartialOutput, ToolProgress } from "../../lib/api-types";

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
  reasoning?: ReasoningEntry[];
  partialOutputs?: PartialOutput[];
  toolProgress?: ToolProgress[];
  messagesRef: RefObject<HTMLElement | null>;
  onCancel: () => void;
  onContinue: (plan?: string, requirements?: AgentRoutingRequirements) => void;
  onHumanInputChange: (value: string) => void;
  onModeChange: (mode: ChatMode) => void;
  onPanelOpenChange: (open: boolean) => void;
  onPromptSelect: (prompt: string) => void;
  onResume: (value?: string) => void;
  onRoleSelect: (role: string) => void;
  onRoutingRequirementsChange: (requirements: AgentRoutingRequirements) => void;
  onTaskStateClose: () => void;
  onTaskStateRefresh: () => void;
  onPlanDraftChange: (value: string) => void;
  planDraft: string;
  routingRequirements: AgentRoutingRequirements;
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
    onRoutingRequirementsChange,
    onTaskStateClose,
    onTaskStateRefresh,
    planDraft,
    routingRequirements,
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
            messages.map((message, index) => (
              <Fragment key={message.id}>
                {message.role === "assistant" ? <ReasoningDisclosure
                  entries={index === messages.length - 1 && props.reasoning?.length ? props.reasoning : message.reasoning ?? []}
                  runStatus={index === messages.length - 1 ? runStatus : "completed"}
                /> : null}
                <article className={`message ${message.role}`}>
                  <div className="message-meta">{message.role}</div>
                  <div className="bubble">
                    {message.content ? renderMarkdown(message.content) : (
                      message.role === "assistant" && isStreaming && index === messages.length - 1 ? (
                        <span className="message-pending" role="status">
                          <LoaderCircle aria-hidden="true" className="is-spinning" size={14} />
                          Working...
                        </span>
                      ) : null
                    )}
                    <MessageCitations citations={message.citations} webCitations={message.web_citations} />
                  </div>
                </article>
              </Fragment>
            ))
          )}
          <PartialOutputPanel outputs={props.partialOutputs ?? []} runStatus={runStatus} />
          <ToolProgressPanel items={props.toolProgress ?? []} runStatus={runStatus} />
          {messages.at(-1)?.role !== "assistant" && props.reasoning?.length && !props.partialOutputs?.some(item => item.channel === "reasoning" && item.text) ? (
            <ReasoningDisclosure entries={props.reasoning} runStatus={runStatus} />
          ) : null}
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
            routingRequirements={routingRequirements}
            runStatus={runStatus}
            selectedRole={selectedRole}
            setPlanDraft={onPlanDraftChange}
            setRoutingRequirements={onRoutingRequirementsChange}
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
