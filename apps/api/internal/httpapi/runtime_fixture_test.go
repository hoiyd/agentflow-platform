package httpapi

import (
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelroute"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/artifact"
)

// Fixture shorthand never participates in production Runtime construction.
func newRuntimeForTest(options agent.RuntimeOptions, client provider.Client) *agent.Runtime {
	if options.Store == nil {
		options.Store = fixturestore.New()
	}
	if client == nil {
		client = openai.NewSimulatedClient()
	}
	if options.ModelRoutes == nil {
		routes, err := modelroute.Catalog(client, options.ContextAssembly, options.RunBudget)
		if err != nil {
			panic(err)
		}
		options.ModelRoutes = routes
	}
	if options.EmbeddingClient == nil {
		options.EmbeddingClient = client
	}
	if options.Tools == nil {
		manager, err := tool.NewManager("")
		if err != nil {
			panic(err)
		}
		options.Tools = manager
	}
	if options.CheckpointProvider == nil {
		options.CheckpointProvider = checkpoint.NewInternalProvider(options.Store)
	}
	if storage, ok := options.Store.(taskstate.Store); ok {
		options.TaskStates = taskstate.NewService(storage, event.StoreSink{Store: options.Store})
	}
	if storage, ok := options.Store.(store.ToolArtifactStore); ok {
		options.ToolArtifacts = artifact.NewService(storage, event.NewRecorder(options.Store))
	}
	if options.KnowledgeRetriever == nil {
		if storage, ok := options.Store.(rag.SearchStore); ok {
			options.KnowledgeRetriever = rag.NewRetrievalPipeline(storage)
		}
	}
	runtime, err := agent.NewRuntime(options)
	if err != nil {
		panic(err)
	}
	return runtime
}
