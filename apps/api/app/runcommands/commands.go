// Package runcommands coordinates admitted execution independently of transport.
// Runtime, RunController and runcompletion remain the execution/state owners.
package runcommands

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

type Runtime interface {
	runcompletion.Lifecycle
	PrepareChatRunWithContract(context.Context, string, string, *domain.CompletionContract) (agent.PreparedRun, error)
	PrepareCollaborationRunWithContract(context.Context, string, string, *domain.CompletionContract) (agent.PreparedCollaborationRun, error)
	PrepareAutonomousRunWithContract(context.Context, string, string, *domain.CompletionContract) (agent.PreparedCollaborationRun, error)
	StreamChat(context.Context, agent.PreparedRun, []domain.Message, string) (<-chan domain.RunEvent, <-chan error)
	RunCollaboration(context.Context, agent.PreparedCollaborationRun, string) (<-chan domain.RunEvent, <-chan error)
	ContinueCollaboration(context.Context, string, string, domain.AgentRoutingRequirements) (<-chan domain.RunEvent, <-chan error)
	RunAutonomous(context.Context, agent.PreparedCollaborationRun, string) (<-chan domain.RunEvent, <-chan error)
	ResumeAutonomous(context.Context, string, string) (<-chan domain.RunEvent, <-chan error)
	ResumeRecoverableAutonomous(context.Context, string, string) (<-chan domain.RunEvent, <-chan error)
	ResumeRecoverableCollaboration(context.Context, string) (<-chan domain.RunEvent, <-chan error)
}

type Capacity interface {
	Reserve() (*concurrency.Reservation, error)
}

// Scope must come from an authorized caller, never from the command payload.
type Scope struct {
	Workspace domain.WorkspaceScope
	Owner     string
}

// An absent Observer is headless execution. Subscriber failures do not cancel work.
type Observer struct {
	Conversation func(string)
	Run          func(domain.Run)
	Event        func(domain.RunEvent)
	Done         func(domain.ChatChunk)
}

type Commands struct {
	Store        store.WorkspaceStoreProvider
	Runtime      Runtime
	Capacity     Capacity
	Verification runcompletion.Verifier
	Memory       memory.TurnSyncer
	AfterRelease func(context.Context, Scope, string)
}

type Reason string

const (
	Invalid          Reason = "invalid"
	Missing          Reason = "missing"
	Conflict         Reason = "conflict"
	Routing          Reason = "routing"
	Overload         Reason = "overload"
	QueueUnavailable Reason = "queue_unavailable"
)

type Error struct {
	Reason Reason
	Err    error
}

func (e *Error) Error() string                    { return e.Err.Error() }
func (e *Error) Unwrap() error                    { return e.Err }
func commandError(reason Reason, err error) error { return &Error{Reason: reason, Err: err} }

func (c Commands) reserve(existing *concurrency.Reservation) (*concurrency.Reservation, error) {
	if existing != nil {
		return existing, nil
	}
	reservation, err := c.Capacity.Reserve()
	if err != nil {
		return nil, commandError(Overload, err)
	}
	return reservation, nil
}

func acquire(ctx context.Context, reservation *concurrency.Reservation, conversation string) (func(), error) {
	release, err := reservation.Start(ctx, conversation)
	if err == nil || ctx.Err() != nil {
		return release, err
	}
	reason := QueueUnavailable
	if errors.Is(err, concurrency.ErrQueueFull) {
		reason = Overload
	}
	return nil, commandError(reason, err)
}

func (c Commands) afterRelease(ctx context.Context, scope Scope, conversation string) {
	if c.AfterRelease != nil {
		c.AfterRelease(context.WithoutCancel(ctx), scope, conversation)
	}
}

func consume(events <-chan domain.RunEvent, errs <-chan error, observer Observer) (string, error) {
	var answer strings.Builder
	for event := range events {
		if event.Type == domain.EventModelDelta {
			if reset, _ := event.Payload["reset"].(bool); reset {
				answer.Reset()
			}
			if delta, ok := event.Payload["delta"].(string); ok {
				answer.WriteString(delta)
			}
		}
		if observer.Event != nil {
			observer.Event(event)
		}
	}
	return answer.String(), <-errs
}

func terminal(observer Observer, run domain.Run) {
	if observer.Done != nil {
		observer.Done(domain.ChatChunk{Type: "done", ConversationID: run.ConversationID, RunID: run.ID, AgentID: run.AgentID, Status: string(run.Status), VerificationStatus: string(run.VerificationStatus)})
	}
}

func (c Commands) complete(ctx context.Context, scoped store.WorkspaceStore, request runcompletion.Request, observer Observer) error {
	chunk, err := runcompletion.Complete(ctx, scoped, runcompletion.Dependencies{Runtime: c.Runtime, Verification: c.Verification}, request)
	if err != nil {
		return err
	}
	if observer.Done != nil {
		observer.Done(chunk)
	}
	if request.UserMessage != nil {
		runcompletion.SyncMemoryTurn(c.Memory, *request.UserMessage, request.RunID)
	}
	return nil
}

// Only a durable Stop is successful cancellation; provider cancellation remains a failure.
func (c Commands) fail(scoped store.WorkspaceStore, runID string, err error, failRun bool, observer Observer) error {
	run, found, loadErr := scoped.GetRun(runID)
	if loadErr != nil {
		return loadErr
	}
	if found && run.Status == domain.RunCanceled {
		terminal(observer, run)
		return nil
	}
	if failRun {
		settled, settleErr := c.Runtime.FailRun(runID, err)
		if settleErr != nil {
			return settleErr
		}
		if settled.Status == domain.RunCanceled {
			terminal(observer, settled)
			return nil
		}
	}
	return err
}
