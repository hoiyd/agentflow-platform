import Link from "next/link";
import type { ReactNode } from "react";
import {
  Activity,
  BrainCircuit,
  ClipboardList,
  Database,
  Menu,
  MessageSquare,
  PanelLeftClose,
  PanelLeftOpen,
  Pencil,
  Plus,
  ShieldCheck,
  Square,
  Trash2,
  Wrench,
  X
} from "lucide-react";

import type { Conversation, ToolInfo } from "../../lib/api";

export type ChatView = "chat" | "tools" | "knowledge" | "memory";

export type VisibleRunState = {
  id: string;
  status: string;
  verificationStatus: string;
};

type SidebarProps = {
  activeId: string;
  conversations: Conversation[];
  isBusy: boolean;
  isCollapsed: boolean;
  isOpen: boolean;
  onCollapseChange: () => void;
  onDeleteConversation: (id: string) => void;
  onNewConversation: () => void;
  onOpenConversation: (id: string) => void;
  onOpenChange: (open: boolean) => void;
  onViewChange: (view: ChatView) => void;
  onViewRefresh: (view: ChatView) => void;
  view: ChatView;
};

export function Sidebar({
  activeId,
  conversations,
  isBusy,
  isCollapsed,
  isOpen,
  onCollapseChange,
  onDeleteConversation,
  onNewConversation,
  onOpenConversation,
  onOpenChange,
  onViewChange,
  onViewRefresh,
  view
}: SidebarProps) {
  function selectView(nextView: ChatView) {
    onViewChange(nextView);
    onOpenChange(false);
    onViewRefresh(nextView);
  }

  return (
    <>
      <aside className={`sidebar ${isOpen ? "mobile-open" : ""} ${isCollapsed ? "collapsed" : ""}`}>
        <div className="brand">
          <Link className="workspace-brand" href="/" title="AgentFlow Operations workspace">
            <span className="brand-mark" aria-hidden="true"><span /></span>
            <span><strong>AgentFlow</strong><small>Operations workspace</small></span>
          </Link>
          <button
            aria-label={isCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            className="sidebar-collapse"
            onClick={onCollapseChange}
            title={isCollapsed ? "Expand sidebar" : "Collapse sidebar"}
            type="button"
          >
            {isCollapsed ? <PanelLeftOpen size={17} /> : <PanelLeftClose size={17} />}
          </button>
          <button className="sidebar-close" aria-label="Close navigation" onClick={() => onOpenChange(false)} type="button">
            <X size={18} />
          </button>
        </div>
        <button className="new-chat" title="New conversation" onClick={() => {
          onOpenChange(false);
          onNewConversation();
        }}>
          <Plus size={17} /> <span>New conversation</span>
        </button>
        <div className="sidebar-section">
          <div className="sidebar-section-title">Operate</div>
          <NavButton active={view === "chat"} icon={<MessageSquare size={16} />} label="Chat" onClick={() => selectView("chat")} />
          <NavButton active={view === "tools"} icon={<Wrench size={16} />} label="Tools" onClick={() => selectView("tools")} />
          <NavButton active={view === "memory"} icon={<BrainCircuit size={16} />} label="Memory" onClick={() => selectView("memory")} />
          <NavButton active={view === "knowledge"} icon={<Database size={16} />} label="Knowledge" onClick={() => selectView("knowledge")} />
        </div>
        <div className="sidebar-section-title conversation-section-title">Recent runs</div>
        <div className="conversation-list">
          {conversations.map((conversation) => (
            <div
              className={`conversation-item ${conversation.id === activeId ? "active" : ""}`}
              key={conversation.id}
              role="button"
              tabIndex={0}
              onClick={() => {
                onOpenChange(false);
                onOpenConversation(conversation.id);
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  onOpenConversation(conversation.id);
                }
              }}
            >
              <div className="conversation-item-main">
                <span className="conversation-title" title={conversation.title}>{conversation.title}</span>
                <span className="conversation-date">
                  {new Date(conversation.updated_at).toLocaleDateString(undefined, { month: "short", day: "numeric" })}
                </span>
              </div>
              <button
                aria-label={`Delete conversation ${conversation.title}`}
                className="conversation-delete"
                disabled={isBusy}
                onClick={(event) => {
                  event.stopPropagation();
                  onDeleteConversation(conversation.id);
                }}
                type="button"
              >
                <Trash2 size={14} />
              </button>
            </div>
          ))}
        </div>
        <div className="sidebar-runtime">
          <span><i /> <span className="sidebar-runtime-label">API connected</span></span>
          <code>v0.7</code>
        </div>
      </aside>
      <button
        aria-label="Close navigation"
        className={`sidebar-scrim ${isOpen ? "visible" : ""}`}
        onClick={() => onOpenChange(false)}
        type="button"
      />
    </>
  );
}

function NavButton({ active, icon, label, onClick }: { active: boolean; icon: ReactNode; label: string; onClick: () => void }) {
  return (
    <button className={`nav-button ${active ? "active" : ""}`} title={label} onClick={onClick}>
      {icon} <span>{label}</span>
    </button>
  );
}

type TopbarProps = {
  activeConversation?: Conversation;
  canCancelRun: boolean;
  conversationTitleDraft: string;
  documentCount: number;
  editingConversationId: string;
  isCancelingRun: boolean;
  isRunStreaming: boolean;
  isSavingConversationTitle: boolean;
  memoryStatus: string;
  onCancelEdit: () => void;
  onCancelRun: () => void;
  onConversationTitleDraftChange: (value: string) => void;
  onOpenNavigation: () => void;
  onTaskStateToggle: () => void;
  onSaveTitle: () => void;
  onStartEdit: (conversation: Conversation) => void;
  runState: VisibleRunState | null;
  taskStateOpen: boolean;
  taskStateVersion: number;
  toolCount: number;
  view: ChatView;
};

export function Topbar({
  activeConversation,
  canCancelRun,
  conversationTitleDraft,
  documentCount,
  editingConversationId,
  isCancelingRun,
  isRunStreaming,
  isSavingConversationTitle,
  memoryStatus,
  onCancelEdit,
  onCancelRun,
  onConversationTitleDraftChange,
  onOpenNavigation,
  onTaskStateToggle,
  onSaveTitle,
  onStartEdit,
  runState,
  taskStateOpen,
  taskStateVersion,
  toolCount,
  view
}: TopbarProps) {
  return (
    <header className="topbar">
      <button className="mobile-menu" aria-label="Open navigation" onClick={onOpenNavigation} type="button">
        <Menu size={19} />
      </button>
      {view === "chat" && activeConversation ? (
        editingConversationId === activeConversation.id ? (
          <form className="conversation-title-editor" onSubmit={(event) => { event.preventDefault(); onSaveTitle(); }}>
            <input
              aria-label="Conversation title"
              autoFocus
              disabled={isSavingConversationTitle}
              onChange={(event) => onConversationTitleDraftChange(event.target.value)}
              onKeyDown={(event) => { if (event.key === "Escape") onCancelEdit(); }}
              value={conversationTitleDraft}
            />
            <button disabled={isSavingConversationTitle || !conversationTitleDraft.trim()} type="submit">Save</button>
            <button disabled={isSavingConversationTitle} onClick={onCancelEdit} type="button">Cancel</button>
          </form>
        ) : (
          <div className="conversation-title-display">
            <div><span className="topbar-eyebrow">Conversation</span><h2>{activeConversation.title}</h2></div>
            <button
              aria-label="Rename conversation"
              className="conversation-title-edit"
              disabled={isSavingConversationTitle}
              onClick={() => onStartEdit(activeConversation)}
              type="button"
            >
              <Pencil size={14} />
            </button>
          </div>
        )
      ) : (
        <div className="topbar-heading">
          <span className="topbar-eyebrow">Workspace</span>
          <h2>{view === "tools" ? "Tools" : view === "memory" ? "Memory" : view === "knowledge" ? "Knowledge" : "New conversation"}</h2>
        </div>
      )}
      <div className="topbar-actions">
        {view === "chat" && activeConversation ? (
          <button
            aria-expanded={taskStateOpen}
            className={`task-state-toggle ${taskStateOpen ? "active" : ""}`}
            onClick={onTaskStateToggle}
            type="button"
          >
            <ClipboardList size={14} />
            <span>Task state</span>
            <code>v{taskStateVersion}</code>
          </button>
        ) : null}
        {view === "chat" && runState ? <RunStatus runState={runState} /> : null}
        {view === "chat" && runState && runState.verificationStatus !== "not_required" ? (
          <span
            aria-label={`Verification status: ${runState.verificationStatus.replaceAll("_", " ")}`}
            className={`run-status-indicator verification-status-indicator ${runState.verificationStatus}`}
          >
            <ShieldCheck size={13} /><span>Verification</span>
            <strong>{runState.verificationStatus.replaceAll("_", " ")}</strong>
          </span>
        ) : null}
        {view === "chat" && canCancelRun ? (
          <button
            className="topbar-run-stop"
            disabled={isCancelingRun || runState?.status === "canceling"}
            onClick={onCancelRun}
            type="button"
          >
            <Square size={12} fill="currentColor" />
            {isCancelingRun || runState?.status === "canceling" ? "Stopping" : "Stop"}
          </button>
        ) : null}
        {view === "chat" && runState?.id ? (
          <a className="run-link" href={`/runs/${runState.id}`}><Activity size={15} /> View trace</a>
        ) : null}
        {view !== "chat" || !runState ? (
          <span className={`status ${isRunStreaming ? "active" : ""}`}>
            <i />
            {view === "tools" ? `${toolCount} enabled` : view === "memory" ? memoryStatus : view === "knowledge" ? `${documentCount} documents` : isRunStreaming ? "Streaming..." : "Ready"}
          </span>
        ) : null}
      </div>
    </header>
  );
}

function RunStatus({ runState }: { runState: VisibleRunState }) {
  const label = runState.status.replaceAll("_", " ");
  return (
    <span aria-label={`Task status: ${label}`} className={`run-status-indicator ${runState.status}`} title={`Run ${runState.id}`}>
      <i /><span>Task</span><strong>{label}</strong>
    </span>
  );
}

export function ToolsPanel({ error, onToggle, tools, updatingTool }: { error: string; onToggle: (tool: ToolInfo) => void; tools: ToolInfo[]; updatingTool: string }) {
  return (
    <section className="tools-panel">
      {error ? <div className="error">{error}</div> : null}
      <div className="tools-list">
        {tools.map((tool) => (
          <article className="tool-card" key={tool.name}>
            <div className="tool-card-header">
              <div><h3>{tool.name}</h3></div>
              <label className="tool-toggle">
                <input type="checkbox" checked={tool.enabled} disabled={updatingTool === tool.name} onChange={() => onToggle(tool)} />
                <span>{tool.enabled ? "Enabled" : "Disabled"}</span>
              </label>
            </div>
            <p>{tool.description}</p>
            <pre>{JSON.stringify(tool.parameters, null, 2)}</pre>
          </article>
        ))}
      </div>
    </section>
  );
}
