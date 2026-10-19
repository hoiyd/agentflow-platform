package runcommands

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/planning"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/store"
)

type Followup struct {
	Input        domain.RunInput
	AllowStopped bool
}

func (c Commands) Start(ctx context.Context, scope Scope, req domain.ChatRequest, reservation *concurrency.Reservation, followup *Followup, observer Observer) error {
	if req.ExecutionConstraint != "" && req.ExecutionConstraint != domain.ExecutionPlanOnly {
		return commandError(Invalid, errors.New("unsupported execution_constraint"))
	}
	if req.PlanOrigin != nil && (req.ConversationID == "" || followup != nil || req.ExecutionConstraint != "") {
		return commandError(Invalid, errors.New("plan execution requires its existing Conversation and a separate explicit request"))
	}
	if scope.Owner == "" || req.WorkspaceID != "" && req.WorkspaceID != scope.Workspace.ID() {
		return commandError(Invalid, errors.New("workspace_id does not match authorized scope"))
	}
	req.WorkspaceID = scope.Workspace.ID()
	knowledgeScope, err := eligibility.FreezeScope(req.KnowledgeFilter)
	if err != nil {
		return commandError(Invalid, err)
	}
	if followup != nil {
		previous, ok, loadErr := c.Store.ForWorkspace(scope.Workspace).GetRun(followup.Input.RunID)
		if loadErr != nil {
			return loadErr
		}
		if !ok {
			return commandError(Missing, store.ErrNotFound("run"))
		}
		filter, loadErr := eligibility.FrozenFilter(previous.RuntimeSnapshot)
		if loadErr != nil {
			return commandError(Invalid, loadErr)
		}
		knowledgeScope, err = eligibility.FreezeScope(filter)
		if err != nil {
			return commandError(Invalid, err)
		}
	}
	ctx = agent.WithKnowledgeScope(ctx, knowledgeScope)
	if err := redaction.ValidateValue(req); err != nil {
		return commandError(Invalid, err)
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		return commandError(Invalid, errors.New("message is required"))
	}
	scoped := c.Store.ForWorkspace(scope.Workspace)
	req.AgentID = strings.TrimSpace(req.AgentID)
	if req.AgentID != "" {
		selected, found, err := scoped.GetAgent(req.AgentID)
		if err != nil {
			return err
		}
		if !found || selected.Archived {
			return commandError(Missing, store.ErrNotFound("agent"))
		}
	}
	contract, err := runcompletion.FreezeContract(c.Verification, req.CompletionContract)
	if err != nil {
		return commandError(Invalid, err)
	}
	req.CompletionContract = contract
	reservation, err = c.reserve(reservation)
	if err != nil {
		return err
	}
	defer reservation.Cancel()
	conversation := strings.TrimSpace(req.ConversationID)
	if conversation == "" {
		created, err := scoped.CreateConversation(req.Message)
		if err != nil {
			return err
		}
		conversation = created.ID
	} else if _, found, err := scoped.GetConversation(conversation); err != nil {
		return err
	} else if !found {
		return commandError(Missing, store.ErrNotFound("conversation"))
	}
	release, err := acquire(ctx, reservation, conversation)
	if err != nil {
		return err
	}
	started := false
	defer func() {
		release()
		if followup == nil || started {
			c.afterRelease(ctx, scope, conversation)
		}
	}()
	var message domain.Message
	if err := c.resolvePlan(ctx, scoped, &req, conversation); err != nil {
		return err
	}
	ctx = agent.WithExecutionIntent(ctx, req.ExecutionConstraint, req.PlanOrigin)
	if followup == nil {
		message, err = scoped.AddMessage(conversation, "user", req.Message)
	} else {
		// Runtime atomically commits the deterministic message, receipt and Run.
		message = domain.Message{ID: followup.Input.ID, WorkspaceID: req.WorkspaceID, ConversationID: conversation, Role: "user", Content: req.Message, CreatedAt: followup.Input.CreatedAt}
		ctx = agent.WithFollowupInput(ctx, followup.Input.ID, scope.Owner, followup.AllowStopped)
	}
	if err != nil {
		return err
	}
	history, err := scoped.ListMessages(conversation)
	if err != nil {
		return err
	}
	if observer.Conversation != nil {
		observer.Conversation(conversation)
	}
	// Only admitted work detaches; queued requests still honor caller cancellation.
	ctx = context.WithoutCancel(ctx)
	mode := agent.NormalizeChatMode(req.Mode)
	var run domain.Run
	var events <-chan domain.RunEvent
	var errs <-chan error
	if mode == agent.ChatModeMultiAgent || mode == agent.ChatModeAutonomous {
		var prepared agent.PreparedCollaborationRun
		if mode == agent.ChatModeMultiAgent {
			prepared, err = c.Runtime.PrepareCollaborationRunWithContract(ctx, req.AgentID, conversation, contract)
		} else {
			prepared, err = c.Runtime.PrepareAutonomousRunWithContract(ctx, req.AgentID, conversation, contract)
		}
		if err == nil {
			run = prepared.Run
			initial := run
			initial.AgentID = prepared.WorkerAgent.ID
			if observer.Run != nil {
				observer.Run(initial)
			}
			started = true
			if mode == agent.ChatModeMultiAgent {
				events, errs = c.Runtime.RunCollaboration(ctx, prepared, req.Message)
			} else {
				events, errs = c.Runtime.RunAutonomous(ctx, prepared, req.Message)
			}
		}
	} else {
		var prepared agent.PreparedRun
		prepared, err = c.Runtime.PrepareChatRunWithContract(ctx, req.AgentID, conversation, contract)
		if err == nil {
			run = prepared.Run
			initial := run
			initial.AgentID = prepared.Agent.ID
			if observer.Run != nil {
				observer.Run(initial)
			}
			started = true
			events, errs = c.Runtime.StreamChat(ctx, prepared, history, req.Message)
		}
	}
	if err != nil {
		if errors.Is(err, planning.ErrAlreadyStarted) {
			return commandError(Conflict, err)
		}
		if store.IsNotFound(err) {
			return commandError(Missing, err)
		}
		return err
	}
	answer, err := consume(events, errs, observer)
	if err != nil {
		if errors.Is(err, clarification.Pending) {
			return c.paused(scoped, run.ID, observer)
		}
		return c.fail(scoped, run.ID, err, true, observer)
	}
	current, found, err := scoped.GetRun(run.ID)
	if err != nil {
		if mode != agent.ChatModeSingle {
			_, _ = c.Runtime.FailRun(run.ID, err)
		}
		return err
	}
	if !found && mode != agent.ChatModeSingle {
		return commandError(Missing, store.ErrNotFound("run"))
	}
	if found && (current.Status == domain.RunWaitingForUser || mode == agent.ChatModeAutonomous && current.Status == domain.RunCanceled) {
		terminal(observer, current)
		if current.RuntimeSnapshot == nil || current.RuntimeSnapshot.ExecutionConstraint != domain.ExecutionPlanOnly {
			runcompletion.SyncMemoryTurn(c.Memory, message, current.ID)
		}
		return nil
	}
	var syncMessage *domain.Message
	if req.ExecutionConstraint != domain.ExecutionPlanOnly {
		syncMessage = &message
	}
	return c.complete(ctx, scoped, runcompletion.Request{WorkspaceID: req.WorkspaceID, RunID: run.ID, ConversationID: conversation, UserInput: req.Message, Assistant: answer, UserMessage: syncMessage, GenerateTitle: true}, observer)
}
