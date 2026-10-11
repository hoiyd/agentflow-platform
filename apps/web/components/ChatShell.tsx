"use client";

import { FormEvent, useEffect, useRef, useState } from "react";
import {
  ChatMode,
  createConversation,
  deleteConversation as deleteConversationApi,
  getAPIHealth,
  listConversations,
} from "../lib/api";
import { buildCompletionContract } from "../lib/verification";
import { Sidebar, ToolsPanel, Topbar, type APIConnectionStatus, type ChatView } from "./chat/ChatChrome";
import { ChatComposer } from "./chat/ChatComposer";
import { RunInbox } from "./chat/RunInbox";
import { ChatDialogs } from "./chat/ChatDialogs";
import { ChatWorkspace } from "./chat/ChatWorkspace";
import { isTerminalRunStatus } from "./chat/runEventProjection";
import { useAgentManagement } from "./chat/useAgentManagement";
import { useCompletionVerification } from "./chat/useCompletionVerification";
import { useConversationWorkspace } from "./chat/useConversationWorkspace";
import { useConversationHistory } from "./chat/useConversationHistory";
import { useToolCatalog } from "./chat/useToolCatalog";
import { useRunSession } from "./chat/useRunSession";
import { KnowledgePanel } from "./knowledge/KnowledgePanel";
import { useKnowledgeWorkbench } from "./knowledge/useKnowledgeWorkbench";
import { MemoryPanel } from "./memory/MemoryPanel";
import { useMemoryWorkbench } from "./memory/useMemoryWorkbench";
import { OperatorAttentionPanel } from "./attention/OperatorAttentionPanel";

type ChatShellProps = {
  initialConversationId?: string;
  initialView?: ChatView;
};

type ChatSidePanel = "trace" | "task_state" | "closed";

export function ChatShell({ initialConversationId = "", initialView = "chat" }: ChatShellProps) {
  const workspace = useConversationWorkspace();
  const {
    conversations, setConversations, activeId, setActiveId, activeConversation,
    messages, setMessages, input, setInput, error, setError,
    editingConversationId, conversationTitleDraft, isSavingConversationTitle,
    startEditingTitle, cancelEditingTitle, setConversationTitleDraft, saveTitle
  } = workspace;
  const [chatMode, setChatMode] = useState<ChatMode>("multi_agent");
  const [sidePanel, setSidePanel] = useState<ChatSidePanel>("trace");
  const [view, setView] = useState<ChatView>(initialView);
  const [isSidebarOpen, setIsSidebarOpen] = useState(false);
  const [isSidebarCollapsed, setIsSidebarCollapsed] = useState(false);
  const [apiConnectionStatus, setAPIConnectionStatus] = useState<APIConnectionStatus>("checking");
  const messagesRef = useRef<HTMLElement | null>(null);
  const knowledge = useKnowledgeWorkbench();
  const memory = useMemoryWorkbench();

  const session = useRunSession({
    workspace,
    observing: view === "chat",
    onConversationAccepted: () => conversationRequests.cancel(),
    onReload: (conversationId, command) => command === "continuing" || command === "resuming"
      ? loadConversation(conversationId)
      : refreshConversations(conversationId),
    onSubmissionStart: (mode) => {
      const previous = sidePanel;
      setSidePanel(mode === "multi_agent" || mode === "autonomous" ? "trace" : "closed");
      return () => setSidePanel(previous);
    }
  });
  const {
    runState, setRunState, isStreaming, isContinuingRun, isResumingRun, isCancelingRun,
    continuePlan: handleContinuePlan, resume: handleResumeAutonomous, cancel: handleCancelRun
  } = session;
  const {
    collaborationSteps, autonomousProgress,
    humanInputDraft, setHumanInputDraft, selectedCollaborationRole, setSelectedCollaborationRole,
    planDraft, setPlanDraft, routingRequirements, setRoutingRequirements
  } = session.trace;
  const verification = useCompletionVerification();
  const toolCatalog = useToolCatalog();
  const {
    agents, workspaceAgents, agentTemplates, newAgentTemplateId, selectNewAgentTemplate,
    activeAgent, activeAgentId, isAgentDescriptionExpanded, agentsError, skills, skillsError, refreshSkills,
    isAgentConfigOpen, agentConfigDraft, newAgentDraft, isNewAgentFormOpen,
    isSavingAgentConfig, isCreatingAgent, archivingAgentId, agentArchiveCandidate,
    agentOperationNotice, agentConfigStatus, refreshAgents, closeAgentForms,
    selectAgent, openAgentConfig, toggleDescription, dismissNotice,
    updateAgentConfigDraft, updateNewAgentDraft, toggleAgentConfigTool, toggleNewAgentTool,
    handleSaveAgentConfig, handleOpenNewAgentForm, handleCancelNewAgent, handleCancelAgentConfig,
    handleCreateAgent, handleArchiveAgent, confirmArchiveAgent, cancelArchiveAgent
  } = useAgentManagement(isStreaming, () => setRunState(null));

  const { conversationRequests, activateConversation, refreshConversations, loadConversation, refreshTaskState,
    taskState, taskStateError, isTaskStateLoading, clearTaskState } = useConversationHistory({
    workspace, session, onModeRestored: handleChatModeChange
  });

  const showCollaborationPanel = chatMode === "multi_agent" || chatMode === "autonomous";
  const showCollaborationDag = chatMode === "multi_agent";
  const showAutonomousTrace = chatMode === "autonomous";
  const isCollaborationPanelOpen = showCollaborationPanel && sidePanel === "trace";
  const isTaskStatePanelOpen = sidePanel === "task_state";
  const useExpandedConversationWidth =
    !isTaskStatePanelOpen && (chatMode === "single" || (showCollaborationPanel && !isCollaborationPanelOpen));
  const isAwaitingPlanApproval = chatMode === "multi_agent" && runState?.status === "waiting_for_user";
  const isAwaitingHumanInput =
    chatMode === "autonomous" &&
    runState?.status === "waiting_for_user" &&
    collaborationSteps.some((step) => step.role === "human_input" && step.status === "running");
  const isTerminalRun = !!runState && isTerminalRunStatus(runState.status);
  const canCancelRun =
    !!runState?.id &&
    !isTerminalRun &&
    (isStreaming ||
      runState.status === "running" ||
      runState.status === "canceling" ||
      runState.status === "waiting_for_user");
  const isRunStreaming =
    isStreaming ||
    isContinuingRun ||
    isResumingRun ||
    runState?.status === "running" ||
    runState?.status === "canceling";
  const visibleCollaborationRole =
    selectedCollaborationRole === "planner" ||
    collaborationSteps.some((step) => step.role === selectedCollaborationRole)
      ? selectedCollaborationRole
      : "planner";

  useEffect(() => {
    void refreshConversations(initialConversationId || undefined);
    void refreshAgents();
    void refreshSkills();
    void toolCatalog.refresh();
    void knowledge.documents.refreshDocuments();
  }, [initialConversationId]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const controller = new AbortController();
    void getAPIHealth(controller.signal)
      .then(() => setAPIConnectionStatus("connected"))
      .catch(() => {
        if (!controller.signal.aborted) setAPIConnectionStatus("unavailable");
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const container = messagesRef.current;
    if (!container) {
      return;
    }
    container.scrollTo({ top: container.scrollHeight, behavior: "smooth" });
  }, [messages]);

  function handleChatModeChange(mode: ChatMode, preserveTaskState = false) {
    setChatMode(mode);
    setSidePanel((current) =>
      preserveTaskState && current === "task_state"
        ? current
        : mode === "multi_agent" || mode === "autonomous"
          ? "trace"
          : "closed"
    );
    if (mode !== "single") {
      closeAgentForms();
    }
  }

  function resetConversationRuntimeState() {
    session.clearRun();
  }

  async function openConversation(id: string) {
    // Returning to the active conversation changes the view, not its Run session.
    if (id === activeId) {
      setView("chat");
      return;
    }
    activateConversation(id);
    setView("chat");
    await loadConversation(id);
  }

  async function startNewConversation() {
    session.detach();
    const request = conversationRequests.begin();
    setError("");
    setView("chat");
    try {
      const conversation = await createConversation("New conversation");
      if (!request.isCurrent()) {
        return;
      }
      setConversations((items) => [conversation, ...items]);
      resetConversationRuntimeState();
      setActiveId(conversation.id);
      setMessages([]);
      await refreshTaskState(conversation.id, request);
    } catch (err) {
      if (request.isCurrent()) {
        setError(err instanceof Error ? err.message : "Failed to create conversation");
      }
    }
  }

  async function handleDeleteConversation(conversationId: string) {
    if (isStreaming || isContinuingRun || isResumingRun) {
      return;
    }
    const confirmed = window.confirm("Delete this conversation? This cannot be undone.");
    if (!confirmed) {
      return;
    }

    setError("");
    const request = conversationRequests.begin();
    try {
      await deleteConversationApi(conversationId);
      if (!request.isCurrent()) {
        setConversations((items) => items.filter((item) => item.id !== conversationId));
        return;
      }
      const items = await listConversations(request.signal);
      if (!request.isCurrent()) {
        setConversations((current) => current.filter((item) => item.id !== conversationId));
        return;
      }
      setConversations(items);

      if (conversationId === activeId) {
        session.detach();
        conversationRequests.cancel();
        const nextConversation = items[0];
        resetConversationRuntimeState();
        setMessages([]);
        clearTaskState();

        if (nextConversation) {
          setActiveId(nextConversation.id);
          setView("chat");
          await loadConversation(nextConversation.id);
        } else {
          setActiveId("");
          setView("chat");
          setSidePanel("closed");
        }
      }
    } catch (err) {
      if (request.isCurrent()) setError(err instanceof Error ? err.message : "Failed to delete conversation");
    }
  }

  function handleOpenCompletionVerification() {
    if (isStreaming) {
      return;
    }
    verification.open();
  }

  async function handleSubmit(event: FormEvent) {
    event.preventDefault();
    const content = input.trim();
    if (!content || isStreaming || isAwaitingPlanApproval || isAwaitingHumanInput || (chatMode === "single" && !activeAgent)) return;
    let completionContract;
    try {
      completionContract = buildCompletionContract(verification.settings);
    } catch (error) {
      setError(error instanceof Error ? error.message : "Invalid verification policy");
      return;
    }
    await session.submit(content, { mode: chatMode, agentId: activeAgentId, completionContract });
  }

  return (
    <div className={`shell ${isSidebarCollapsed ? "sidebar-collapsed" : ""}`}>
      <Sidebar
        activeId={activeId}
        apiConnectionStatus={apiConnectionStatus}
        conversations={conversations}
        isBusy={isStreaming || isContinuingRun}
        isCollapsed={isSidebarCollapsed}
        isOpen={isSidebarOpen}
        onCollapseChange={() => setIsSidebarCollapsed((current) => !current)}
        onDeleteConversation={(id) => void handleDeleteConversation(id)}
        onNewConversation={() => void startNewConversation()}
        onOpenConversation={(id) => void openConversation(id)}
        onOpenChange={setIsSidebarOpen}
        onViewChange={setView}
        onViewRefresh={(nextView) => {
          if (nextView === "tools") void toolCatalog.refresh();
          if (nextView === "knowledge") void knowledge.documents.refreshDocuments();
        }}
        view={view}
      />

      <main className="main">
        <Topbar
          activeConversation={activeConversation}
          canCancelRun={canCancelRun}
          conversationTitleDraft={conversationTitleDraft}
          documentCount={knowledge.documents.documents.length}
          editingConversationId={editingConversationId}
          isCancelingRun={isCancelingRun}
          isRunStreaming={isRunStreaming}
          isSavingConversationTitle={isSavingConversationTitle}
          memoryStatus={memory.hasSearched ? `${memory.results.length} matches` : "Recall ready"}
          onCancelEdit={cancelEditingTitle}
          onCancelRun={() => void handleCancelRun()}
          onConversationTitleDraftChange={setConversationTitleDraft}
          onOpenNavigation={() => setIsSidebarOpen(true)}
          onTaskStateToggle={() => setSidePanel((current) => current === "task_state" ? "closed" : "task_state")}
          onSaveTitle={() => void saveTitle()}
          onStartEdit={startEditingTitle}
          runState={runState}
          taskStateOpen={isTaskStatePanelOpen}
          taskStateVersion={taskState?.version ?? 0}
          toolCount={toolCatalog.tools.filter((tool) => tool.enabled).length}
          view={view}
        />

        {view === "tools" ? (
          <ToolsPanel error={toolCatalog.error} onToggle={(tool) => void toolCatalog.toggle(tool)} tools={toolCatalog.tools} updatingTool={toolCatalog.updatingTool} />
        ) : view === "knowledge" ? (
          <KnowledgePanel model={knowledge} />
        ) : view === "memory" ? (
          <MemoryPanel model={memory} />
        ) : view === "attention" ? (
          <OperatorAttentionPanel />
        ) : (
          <ChatWorkspace
            agents={agents}
            autonomousProgress={autonomousProgress}
            chatMode={chatMode}
            collaborationSteps={collaborationSteps}
            humanInputDraft={humanInputDraft}
            isCanceling={isCancelingRun || runState?.status === "canceling"}
            isCollaborationPanelOpen={isCollaborationPanelOpen}
            isContinuing={isContinuingRun}
            isResuming={isResumingRun}
            isStreaming={isStreaming}
            isTaskStatePanelOpen={isTaskStatePanelOpen}
            messages={messages}
            reasoning={session.reasoning}
            partialOutputs={isStreaming ? [] : session.partialOutputs}
            toolProgress={session.toolProgress}
            messagesRef={messagesRef}
            onCancel={() => void handleCancelRun()}
            onContinue={handleContinuePlan}
            onHumanInputChange={setHumanInputDraft}
            onModeChange={handleChatModeChange}
            onPanelOpenChange={(open) => setSidePanel(open ? "trace" : "closed")}
            onPlanDraftChange={setPlanDraft}
            onPromptSelect={setInput}
            onResume={handleResumeAutonomous}
            onRoleSelect={setSelectedCollaborationRole}
            onRoutingRequirementsChange={setRoutingRequirements}
            onTaskStateClose={() => setSidePanel("closed")}
            onTaskStateRefresh={() => void refreshTaskState()}
            planDraft={planDraft}
            routingRequirements={routingRequirements}
            runStatus={runState?.status ?? ""}
            selectedRole={visibleCollaborationRole}
            showAutonomousTrace={showAutonomousTrace}
            showCollaborationDag={showCollaborationDag}
            showCollaborationPanel={showCollaborationPanel}
            taskState={taskState}
            taskStateError={taskStateError}
            taskStateLoading={isTaskStateLoading}
            useExpandedConversationWidth={useExpandedConversationWidth}
          />
        )}

        {view === "chat" ? (
          <ChatComposer
            inbox={activeId && runState ? <RunInbox key={activeId} conversationId={activeId} runId={runState.id} mode={chatMode} agentId={activeAgentId}
              input={input} busy={isStreaming || ["running","queued","canceling"].includes(runState.status)}
              canStartFollowup={["completed","failed","canceled"].includes(runState.status) && (chatMode !== "single" || Boolean(activeAgent))}
              onSubmitted={()=>setInput("")} onRunAvailable={()=>{if(!isStreaming)void loadConversation(activeId);}} /> : null}
            activeAgent={activeAgent}
            activeAgentId={activeAgentId}
            agents={workspaceAgents}
            agentsError={agentsError}
            chatMode={chatMode}
            completionVerificationEnabled={verification.settings.enabled}
            error={error}
            input={input}
            isAgentDescriptionExpanded={isAgentDescriptionExpanded}
            isAwaitingHumanInput={isAwaitingHumanInput}
            isAwaitingPlanApproval={isAwaitingPlanApproval}
            isCreatingAgent={isCreatingAgent}
            isNewAgentFormOpen={isNewAgentFormOpen}
            isStreaming={isStreaming || Boolean(runState && ["running","queued","canceling"].includes(runState.status))}
            onAgentChange={selectAgent}
            onConfigureAgent={openAgentConfig}
            onDescriptionExpandedChange={toggleDescription}
            onInputChange={setInput}
            onNewAgent={handleOpenNewAgentForm}
            onOpenVerification={handleOpenCompletionVerification}
            onSubmit={handleSubmit}
            showAgentActions
          />
        ) : null}
      </main>
      <ChatDialogs
        agentTemplates={agentTemplates}
        newAgentTemplateId={newAgentTemplateId}
        onNewAgentTemplateChange={selectNewAgentTemplate}
        activeAgent={activeAgent}
        agentArchiveCandidate={agentArchiveCandidate}
        agentConfigDraft={agentConfigDraft}
        agentConfigStatus={agentConfigStatus}
        agentOperationNotice={agentOperationNotice}
        archivingAgentId={archivingAgentId}
        chatMode={chatMode}
        completionVerificationDraft={verification.draft}
        completionVerificationError={verification.error}
        isAgentConfigOpen={isAgentConfigOpen}
        isCreatingAgent={isCreatingAgent}
        isNewAgentFormOpen={isNewAgentFormOpen}
        isSavingAgentConfig={isSavingAgentConfig}
        isStreaming={isStreaming}
        newAgentDraft={newAgentDraft}
        onArchiveAgent={handleArchiveAgent}
        onCancelAgentConfig={handleCancelAgentConfig}
        onCancelArchive={cancelArchiveAgent}
        onCancelNewAgent={handleCancelNewAgent}
        onCompletionVerificationCancel={verification.cancel}
        onCompletionVerificationChange={verification.change}
        onConfirmArchive={() => void confirmArchiveAgent()}
        onCreateAgent={() => void handleCreateAgent()}
        onDismissNotice={dismissNotice}
        onNewAgentChange={updateNewAgentDraft}
        onNewAgentToolToggle={toggleNewAgentTool}
        onSaveAgentConfig={() => void handleSaveAgentConfig()}
        onSaveCompletionVerification={verification.save}
        onUpdateAgentChange={updateAgentConfigDraft}
        onUpdateAgentToolToggle={toggleAgentConfigTool}
        tools={toolCatalog.tools}
        skills={skills}
        skillsError={skillsError}
      />
    </div>
  );
}
