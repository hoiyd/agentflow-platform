package agent

import (
	"testing"

	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelroute"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/artifact"
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
	assertOptionalBindingsAbsent(t, runtime)
	options.KnowledgeTools = []tool.Binding{{Descriptor: tool.Descriptor{Name: "invalid", Parameters: tool.ObjectSchema(nil, nil)}}}
	if _, err := NewRuntime(options); err == nil {
		t.Fatal("invalid harness Binding accepted at startup")
	}
}

type runtimeStoreCapabilitiesOnly struct{ RuntimeStore }

func assertOptionalBindingsAbsent(t *testing.T, runtime *Runtime) {
	t.Helper()
	catalog, err := runtime.currentCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{taskstate.UpdateToolName, artifact.ReadToolName, artifact.SearchToolName} {
		if _, ok := catalog.Installed(name); ok {
			t.Errorf("uninjected optional Binding %s installed", name)
		}
	}
}

// Failure inventory: a Runtime-only facade must hide checkpoint writes and
// optional stores; the production constructor must still require a provider.
func TestRuntimeStoreRequiresOnlyEffectReads(t *testing.T) {
	storage := fixturestore.New()
	wrapped := runtimeStoreCapabilitiesOnly{RuntimeStore: storage}
	_, checkpointProviderStore := any(wrapped).(checkpoint.Store)
	_, checkpointJournal := any(wrapped).(store.CheckpointStore)
	_, taskStateStore := any(wrapped).(taskstate.Store)
	_, artifactStore := any(wrapped).(store.ToolArtifactStore)
	for name, present := range map[string]bool{
		"checkpoint provider store": checkpointProviderStore,
		"checkpoint journal":        checkpointJournal,
		"Task State":                taskStateStore,
		"Artifact":                  artifactStore,
	} {
		if present {
			t.Errorf("RuntimeStore unnecessarily exposes %s", name)
		}
	}
	client := newLocalFallbackOpenAIClientForTest()
	routes, err := modelroute.Catalog(client, domain.ContextAssemblyConfig{}, domain.RuntimeRunBudget{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tool.NewManager("")
	if err != nil {
		t.Fatal(err)
	}
	options := RuntimeOptions{Store: wrapped, ModelRoutes: routes, EmbeddingClient: client, Tools: manager, CheckpointProvider: checkpoint.NewInternalProvider(storage)}
	runtime, err := NewRuntime(options)
	if err != nil {
		t.Fatal(err)
	}
	assertOptionalBindingsAbsent(t, runtime)
	options.CheckpointProvider = nil
	if runtime, err := NewRuntime(options); err == nil || runtime != nil {
		t.Fatal("production constructor accepted missing checkpoint provider")
	}
	t.Run("fixture requires explicit provider for wrapped store", func(t *testing.T) {
		defer func() {
			recovered := recover()
			if err, ok := recovered.(error); !ok || err.Error() != "runtime checkpoint provider is required" {
				t.Fatalf("fixture bypassed required provider: %v", recovered)
			}
		}()
		newRuntimeForTest(options, client)
	})
	options.CheckpointProvider = checkpoint.NewInternalProvider(storage)
	assertOptionalBindingsAbsent(t, newRuntimeForTest(options, client))
}
