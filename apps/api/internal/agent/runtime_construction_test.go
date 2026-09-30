package agent

import (
	"testing"

	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelroute"
	"agentflow-platform/apps/api/internal/tool"
)

func TestRuntimeDoesNotInferOptionalCapabilitiesFromStore(t *testing.T) {
	client := newLocalFallbackOpenAIClientForTest()
	routes, err := modelroute.Catalog(client, domain.ContextAssemblyConfig{}, domain.RuntimeRunBudget{})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := tool.NewManager("")
	if err != nil {
		t.Fatal(err)
	}
	storage := fixturestore.New()
	options := RuntimeOptions{Store: storage, ModelRoutes: routes, EmbeddingClient: client, Tools: tools, CheckpointProvider: checkpoint.NewInternalProvider(storage)}
	runtime, err := NewRuntime(options)
	if err != nil {
		t.Fatal(err)
	}
	for name, remove := range map[string]func(*RuntimeOptions){
		"store":       func(o *RuntimeOptions) { o.Store = nil },
		"routes":      func(o *RuntimeOptions) { o.ModelRoutes = nil },
		"embedding":   func(o *RuntimeOptions) { o.EmbeddingClient = nil },
		"tools":       func(o *RuntimeOptions) { o.Tools = nil },
		"checkpoints": func(o *RuntimeOptions) { o.CheckpointProvider = nil },
	} {
		t.Run(name, func(t *testing.T) {
			incomplete := options
			remove(&incomplete)
			if value, err := NewRuntime(incomplete); err == nil || value != nil {
				t.Fatal("missing dependency accepted")
			}
		})
	}
	if runtime.taskStates != nil || runtime.toolArtifacts != nil || runtime.knowledgeRetriever != nil || len(runtime.knowledgeTools) != 0 {
		t.Fatal("Store implementation silently enabled optional runtime capabilities")
	}
}
