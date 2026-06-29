package temporalrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"

	"go.temporal.io/sdk/activity"
)

const ExecuteAutonomousRunActivityName = "ExecuteAutonomousRunActivity"

type Activities struct {
	Runtime *agent.Runtime
	Store   store.Store
}

func (a *Activities) ExecuteAutonomousRunActivity(ctx context.Context, input AgentRunWorkflowInput) (AgentRunWorkflowResult, error) {
	if a == nil || a.Runtime == nil || a.Store == nil {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, errors.New("temporal activities are not configured")
	}
	if _, err := a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusRunning); err != nil {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
	}
	stopHeartbeat := make(chan struct{})
	defer close(stopHeartbeat)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, input.RunID)
			}
		}
	}()

	run, ok, err := a.Store.GetRun(strings.TrimSpace(input.RunID))
	if err != nil {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
	}
	if !ok {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, fmt.Errorf("run not found")
	}
	workerAgent, ok, err := a.Store.GetAgent(run.AgentID)
	if err != nil {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
	}
	if !ok {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, fmt.Errorf("agent not found")
	}

	prepared := agent.PreparedCollaborationRun{WorkerAgent: workerAgent, Run: run}
	events, errs := a.Runtime.RunAutonomous(ctx, prepared, input.Task)
	if input.ResumeCanceled {
		events, errs = a.Runtime.ResumeCanceledAutonomous(ctx, input.RunID, input.ResumeNote)
	}
	var assistant strings.Builder
	for event := range events {
		if event.Type == "delta" {
			assistant.WriteString(event.Delta)
		}
	}

	if err := <-errs; err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			_, _ = a.Store.UpdateRunStatus(input.RunID, domain.RunCanceled, "canceled by Temporal workflow")
			_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusCanceled)
			return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusCanceled}, nil
		}
		current, ok, getErr := a.Store.GetRun(input.RunID)
		if getErr == nil && ok && current.Status == domain.RunCanceled {
			_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusCanceled)
			return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusCanceled}, nil
		}
		_, _ = a.Runtime.FailRun(input.RunID, err)
		_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusFailed)
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
	}

	current, ok, err := a.Store.GetRun(input.RunID)
	if err != nil {
		_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusFailed)
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
	}
	if !ok {
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, fmt.Errorf("run not found")
	}
	if current.Status == domain.RunWaitingForUser {
		_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, "waiting_for_user")
		return AgentRunWorkflowResult{RunID: input.RunID, Status: "waiting_for_user"}, nil
	}
	if current.Status == domain.RunCanceled {
		_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusCanceled)
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusCanceled}, nil
	}

	if strings.TrimSpace(assistant.String()) != "" {
		if _, err := a.Store.AddMessage(current.ConversationID, "assistant", assistant.String()); err != nil {
			_, _ = a.Runtime.FailRun(input.RunID, err)
			_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusFailed)
			return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
		}
	}
	if _, err := a.Runtime.CompleteRun(input.RunID); err != nil {
		_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusFailed)
		return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusFailed}, err
	}
	_, _ = a.Store.UpdateRunWorkflowStatus(input.RunID, WorkflowStatusCompleted)
	return AgentRunWorkflowResult{RunID: input.RunID, Status: WorkflowStatusCompleted}, nil
}
