import type { Dispatch, SetStateAction } from "react";

import type { ChatEvent, Message } from "../../lib/api";
import {
  toAutonomousProgress,
  upsertCollaborationStep,
  type AutonomousProgress,
  type CollaborationStepView
} from "./CollaborationPanels";

export type DraftMessage = Pick<Message, "role" | "content" | "citations"> & {
  id: string;
  conversation_id: string;
  created_at: string;
};

export type RunState = {
  id: string;
  agentId: string;
  status: string;
  verificationStatus: string;
};

type RunEventProjectionOptions = {
  assistantDraftId: string;
  defaultVerificationStatus: string;
  fallbackAgentId: string;
  fallbackRunId: string;
  onConversation?: (event: Extract<ChatEvent, { type: "conversation" }>) => void;
  onDone?: (event: Extract<ChatEvent, { type: "done" }>) => void;
  onRunState?: (event: Extract<ChatEvent, { type: "run_state" }>) => void;
  setAutonomousProgress: Dispatch<SetStateAction<AutonomousProgress | null>>;
  setCollaborationSteps: Dispatch<SetStateAction<CollaborationStepView[]>>;
  setError: Dispatch<SetStateAction<string>>;
  setIsCancelingRun: Dispatch<SetStateAction<boolean>>;
  setMessages: Dispatch<SetStateAction<DraftMessage[]>>;
  setPlanDraft: Dispatch<SetStateAction<string>>;
  setRunState: Dispatch<SetStateAction<RunState | null>>;
};

export function createRunEventHandler(options: RunEventProjectionOptions) {
  return (event: ChatEvent) => {
    if (event.type === "conversation") {
      options.onConversation?.(event);
    }
    if (event.type === "run_state") {
      options.setRunState((current) => ({
        id: event.run_id,
        agentId: event.agent_id,
        status: event.status,
        verificationStatus: current?.verificationStatus ?? options.defaultVerificationStatus
      }));
      if (isTerminalStatus(event.status)) {
        options.setIsCancelingRun(false);
      }
      options.onRunState?.(event);
    }
    if (event.type === "stage_state") {
      options.setCollaborationSteps((items) => upsertCollaborationStep(items, event));
      if (event.role === "planner" && event.output) {
        options.setPlanDraft(event.output);
      }
    }
    if (event.type === "run_progress") {
      options.setAutonomousProgress(toAutonomousProgress(event));
    }
    if (event.type === "model_delta") {
      options.setMessages((items) =>
        items.map((item) =>
          item.id === options.assistantDraftId ? { ...item, content: item.content + event.delta } : item
        )
      );
    }
    if (event.type === "error") {
      options.setError(event.error);
    }
    if (event.type === "done") {
      options.setMessages((items) =>
        items.map((item) =>
          item.id === options.assistantDraftId ? { ...item, citations: event.citations } : item
        )
      );
      options.setRunState((current) => ({
        id: event.run_id ?? current?.id ?? options.fallbackRunId,
        agentId: event.agent_id ?? current?.agentId ?? options.fallbackAgentId,
        status: event.status ?? "completed",
        verificationStatus:
          event.verification_status ?? current?.verificationStatus ?? options.defaultVerificationStatus
      }));
      options.setIsCancelingRun(false);
      options.onDone?.(event);
    }
  };
}

function isTerminalStatus(status: string) {
  return status === "canceled" || status === "completed" || status === "failed" || status === "failed_recoverable";
}
