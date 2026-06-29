package temporalrun

import (
	"fmt"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/store"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

type WorkerHandle struct {
	worker worker.Worker
}

func NewClient(hostPort string, namespace string) (client.Client, error) {
	return client.Dial(client.Options{
		HostPort:  hostPort,
		Namespace: namespace,
	})
}

func StartWorker(temporalClient client.Client, taskQueue string, runtime *agent.Runtime, appStore store.Store) (*WorkerHandle, error) {
	if temporalClient == nil {
		return nil, fmt.Errorf("temporal client is required")
	}
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	activities := &Activities{Runtime: runtime, Store: appStore}
	w.RegisterWorkflow(AgentRunWorkflow)
	w.RegisterActivityWithOptions(activities.ExecuteAutonomousRunActivity, activity.RegisterOptions{Name: ExecuteAutonomousRunActivityName})
	if err := w.Start(); err != nil {
		return nil, err
	}
	return &WorkerHandle{worker: w}, nil
}

func (h *WorkerHandle) Stop() {
	if h != nil && h.worker != nil {
		h.worker.Stop()
	}
}
