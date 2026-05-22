package agent

import (
	"context"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

type Runtime struct {
	store            store.Store
	openAI           *openai.Client
	tools            *tools.Manager
	routerMode       string
	autonomousLimits AutonomousLimits
}

type AutonomousLimits struct {
	MaxIterations  int
	MaxRuntime     time.Duration
	MaxOutputChars int
	MaxToolCalls   int
}

type PreparedRun struct {
	Agent    domain.Agent
	Run      domain.Run
	Registry *tools.Registry
}

func NewRuntime(store store.Store, openAI *openai.Client, tools *tools.Manager) *Runtime {
	return NewRuntimeWithRouterMode(store, openAI, tools, RouterModeAuto)
}

func NewRuntimeWithRouterMode(store store.Store, openAI *openai.Client, tools *tools.Manager, routerMode string) *Runtime {
	return NewRuntimeWithRouterModeAndLimits(store, openAI, tools, routerMode, DefaultAutonomousLimits())
}

func NewRuntimeWithRouterModeAndLimits(store store.Store, openAI *openai.Client, tools *tools.Manager, routerMode string, limits AutonomousLimits) *Runtime {
	return &Runtime{
		store:            store,
		openAI:           openAI,
		tools:            tools,
		routerMode:       NormalizeRouterMode(routerMode),
		autonomousLimits: normalizeAutonomousLimits(limits),
	}
}

func DefaultAutonomousLimits() AutonomousLimits {
	return AutonomousLimits{
		MaxIterations:  5,
		MaxRuntime:     5 * time.Minute,
		MaxOutputChars: 60000,
		MaxToolCalls:   20,
	}
}

func (r *Runtime) PrepareChatRun(ctx context.Context, agentID string, conversationID string) (PreparedRun, error) {
	agent, err := r.resolveAgent(strings.TrimSpace(agentID))
	if err != nil {
		return PreparedRun{}, err
	}

	run, err := r.store.CreateRun(agent.ID, conversationID)
	if err != nil {
		return PreparedRun{}, err
	}
	run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		return PreparedRun{}, err
	}

	registry, err := r.tools.Registry(ctx)
	if err != nil {
		_, _ = r.FailRun(run.ID, err)
		return PreparedRun{}, err
	}
	agentRegistry, err := registry.EnabledSubset(agent.Tools)
	if err != nil {
		_, _ = r.FailRun(run.ID, err)
		return PreparedRun{}, err
	}

	return PreparedRun{Agent: agent, Run: run, Registry: agentRegistry}, nil
}

func (r *Runtime) StreamChat(ctx context.Context, prepared PreparedRun, history []domain.Message, latest string) (<-chan openai.StreamEvent, <-chan error) {
	return r.openAI.StreamAgentChatWithTools(ctx, prepared.Agent.SystemPrompt, history, latest, prepared.Registry)
}

func (r *Runtime) CompleteRun(id string) (domain.Run, error) {
	return r.store.UpdateRunStatus(id, domain.RunCompleted, "")
}

func (r *Runtime) FailRun(id string, err error) (domain.Run, error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return r.store.UpdateRunStatus(id, domain.RunFailed, message)
}

func (r *Runtime) CancelRun(id string) (domain.Run, error) {
	run, ok, err := r.store.GetRun(strings.TrimSpace(id))
	if err != nil {
		return domain.Run{}, err
	}
	if !ok {
		return domain.Run{}, store.ErrNotFound("run")
	}
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunCanceled:
		return run, nil
	case domain.RunRunning:
		return r.store.UpdateRunStatus(run.ID, domain.RunCanceling, "cancel requested by user")
	default:
		return r.store.UpdateRunStatus(run.ID, domain.RunCanceled, "canceled by user")
	}
}

func (r *Runtime) resolveAgent(agentID string) (domain.Agent, error) {
	if agentID == "" {
		agent, ok, err := r.store.GetDefaultAgent()
		if err != nil {
			return domain.Agent{}, err
		}
		if ok {
			return agent, nil
		}
		return domain.Agent{}, store.ErrNotFound("agent")
	}

	agent, ok, err := r.store.GetAgent(agentID)
	if err != nil {
		return domain.Agent{}, err
	}
	if !ok {
		return domain.Agent{}, store.ErrNotFound("agent")
	}
	return agent, nil
}

func normalizeAutonomousLimits(limits AutonomousLimits) AutonomousLimits {
	defaults := DefaultAutonomousLimits()
	if limits.MaxIterations <= 0 {
		limits.MaxIterations = defaults.MaxIterations
	}
	if limits.MaxRuntime <= 0 {
		limits.MaxRuntime = defaults.MaxRuntime
	}
	if limits.MaxOutputChars <= 0 {
		limits.MaxOutputChars = defaults.MaxOutputChars
	}
	if limits.MaxToolCalls <= 0 {
		limits.MaxToolCalls = defaults.MaxToolCalls
	}
	return limits
}
