package agent

import (
	"context"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

type Runtime struct {
	store  store.Store
	openAI *openai.Client
	tools  *tools.Manager
}

type PreparedRun struct {
	Agent    domain.Agent
	Run      domain.Run
	Registry *tools.Registry
}

func NewRuntime(store store.Store, openAI *openai.Client, tools *tools.Manager) *Runtime {
	return &Runtime{store: store, openAI: openAI, tools: tools}
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
