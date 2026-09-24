package openai

import (
	"context"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
)

func streamToolLoopForTest(client *Client, ctx context.Context, systemPrompt string, history []domain.Message, latest string, catalog *tool.Catalog, recorder *eventpkg.Recorder, runID string, stepID string, memories []domain.RetrievedMemory, knowledge []domain.RetrievedDocumentChunk) (<-chan provider.StreamEvent, <-chan error) {
	return toolloop.Stream(ctx, client, toolloop.Request{
		SystemPrompt: systemPrompt, History: history, Latest: latest, Catalog: catalog,
		Trace: provider.ChatTrace{Recorder: recorder, RunID: runID, StepID: stepID, Memories: memories, Knowledge: knowledge},
	})
}
