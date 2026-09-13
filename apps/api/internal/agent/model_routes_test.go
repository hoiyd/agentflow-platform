package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelrouting"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tools"
	turnpkg "agentflow-platform/apps/api/internal/turn"
)

func TestFrozenModelCatalogIgnoresLaterRoutesAndRetainsIdentity(t *testing.T) {
	original := modelRouteBinding(t, "general", "model-v1", 100, modelrouting.Capabilities{Streaming: true})
	originalCatalog, err := modelrouting.NewCatalog(original)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		EmbeddingClient: original.Client, ModelRoutes: originalCatalog,
		ContextAssembly: domain.ContextAssemblyConfig{ContextWindowTokens: 1000, OutputReserveTokens: 100},
	})
	snapshot, err := runtime.captureRuntimeSnapshot(ChatModeSingle, domain.Agent{ID: "agent"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	changed := modelRouteBinding(t, "general", "model-v2", 100, modelrouting.Capabilities{Streaming: true})
	later := modelRouteBinding(t, "later", "model-v3", 200, modelrouting.Capabilities{Streaming: true})
	runtime.modelRoutes, err = modelrouting.NewCatalog(changed, later)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := runtime.restoreModelRouteCatalog(snapshot.ModelRouting)
	if err != nil {
		t.Fatal(err)
	}
	descriptors := restored.Descriptors()
	if len(descriptors) != 1 || descriptors[0].ID != "general" || descriptors[0].Model != "model-v1" {
		t.Fatalf("resume adopted live route changes: %#v", descriptors)
	}

	runtime.modelRoutes, _ = modelrouting.NewCatalog(later)
	if _, err := runtime.restoreModelRouteCatalog(snapshot.ModelRouting); !errors.Is(err, modelrouting.ErrNoCompatibleRoute) {
		t.Fatalf("missing frozen route should fail closed: %v", err)
	}
}

func TestModelRouteDecisionRecordsNoCandidateEvidence(t *testing.T) {
	binding := modelRouteBinding(t, "text_only", "model-v1", 100, modelrouting.Capabilities{})
	catalog, err := modelrouting.NewCatalog(binding)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		EmbeddingClient: binding.Client, ModelRoutes: catalog,
		ContextAssembly: domain.ContextAssemblyConfig{ContextWindowTokens: 1000, OutputReserveTokens: 100},
	})
	snapshot, err := runtime.captureRuntimeSnapshot(ChatModeSingle, domain.Agent{ID: "agent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var recorded domain.RunEvent
	request := turnpkg.Request{
		RunID: "run-1", ConversationID: "conversation-1", TurnID: "turn-1", Role: "router",
		SystemPrompt: "return JSON", Input: "route this", ModelMode: turnpkg.ModelModeText,
		Sink: eventpkg.SinkFunc(func(_ context.Context, item domain.RunEvent) error { recorded = item; return nil }),
	}
	decision, err := runtime.selectModelRoute(context.Background(), request, &snapshot)
	if !errors.Is(err, modelrouting.ErrNoCompatibleRoute) || len(decision.Candidates) != 1 {
		t.Fatalf("expected typed no-route decision, decision=%#v err=%v", decision, err)
	}
	if recorded.Type != domain.EventModelRouteDecided || recorded.Payload["outcome"] != "no_compatible_route" {
		t.Fatalf("missing route decision event: %#v", recorded)
	}
	candidates, ok := recorded.Payload["candidates"].([]any)
	if !ok || len(candidates) != 1 {
		t.Fatalf("missing candidate evidence: %#v", recorded.Payload)
	}
}

func TestModelRequirementsReflectTurnContract(t *testing.T) {
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly.ContextWindowTokens = 1000
	snapshot.ContextAssembly.OutputReserveTokens = 100
	snapshot.ContextAssembly.SafetyMarginTokens = 50
	requirements := modelRequirements(turnpkg.Request{
		Role: "decide", Agent: domain.Agent{SystemPrompt: "decide as JSON"}, Input: "finish",
		History: []domain.Message{{Role: "user", Content: "history"}}, Catalog: tools.DefaultCatalog(),
	}, &snapshot)
	if requirements.Purpose != "decide" || !requirements.StructuredOutput || !requirements.Streaming || !requirements.ToolCalling || requirements.EstimatedInputTokens <= 0 || requirements.MaxOutputTokens != 100 {
		t.Fatalf("turn requirements lost routing constraints: %#v", requirements)
	}
}

func TestRuntimeTurnModelRoutesCallsAcrossProviders(t *testing.T) {
	var structuredCalls, fastCalls atomic.Int32
	provider := func(calls *atomic.Int32, output string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			if request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer test-key" {
				t.Errorf("unexpected provider request: path=%q authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + output + `"}}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
		}))
	}
	structuredServer := provider(&structuredCalls, "structured")
	fastServer := provider(&fastCalls, "fast")
	t.Cleanup(structuredServer.Close)
	t.Cleanup(fastServer.Close)

	structuredClient := openai.NewClient("test-key", structuredServer.URL+"/v1", "structured-model")
	fastClient := openai.NewClient("test-key", fastServer.URL+"/v1", "fast-model")
	binding := func(id string, priority int, capabilities modelrouting.Capabilities, client *openai.Client) modelrouting.Binding {
		identity := client.RuntimeIdentity()
		return modelrouting.Binding{Descriptor: modelrouting.Descriptor{
			ID: id, Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
			Capabilities: capabilities, ContextWindowTokens: 1000, MaxOutputTokens: 100,
			Priority: priority, Pricing: modelrouting.Pricing{Source: "test_fixture"},
			CredentialEnvironment: "TEST_MODEL_API_KEY",
		}, Client: client}
	}
	catalog, err := modelrouting.NewCatalog(
		binding("structured", 100, modelrouting.Capabilities{StructuredOutput: true}, structuredClient),
		binding("fast", 200, modelrouting.Capabilities{}, fastClient),
	)
	if err != nil {
		t.Fatal(err)
	}
	store := fixturestore.New()
	conversation, _ := store.CreateConversation("model routing")
	config := domain.ContextAssemblyConfig{ContextWindowTokens: 1000, OutputReserveTokens: 100, SafetyMarginTokens: 50}
	runtime := NewRuntime(RuntimeOptions{Store: store, EmbeddingClient: newLocalFallbackOpenAIClientForTest(), ModelRoutes: catalog, ContextAssembly: config})
	agent, err := store.CreateAgent(domain.Agent{Name: "Routing agent", SystemPrompt: "help"})
	if err != nil {
		t.Fatal(err)
	}
	execute := func(role string) string {
		t.Helper()
		snapshot, captureErr := runtime.captureRuntimeSnapshot(ChatModeSingle, agent, nil)
		if captureErr != nil {
			t.Fatal(captureErr)
		}
		run, createErr := store.CreateRunWithContract(agent.ID, conversation.ID, snapshot, nil)
		if createErr != nil {
			t.Fatal(createErr)
		}
		result, executeErr := (runtimeTurnModel{runtime: runtime}).Execute(context.Background(), turnpkg.Request{
			RunID: run.ID, ConversationID: conversation.ID, TurnID: "turn-" + role,
			Agent: agent, Role: role, SystemPrompt: agent.SystemPrompt, Input: "hello", ModelMode: turnpkg.ModelModeText,
		}, func(turnpkg.ModelEvent) {})
		if executeErr != nil {
			t.Fatal(executeErr)
		}
		return result.Output
	}

	if output := execute("answer"); output != "fast" {
		t.Fatalf("priority routing used the wrong provider: %q", output)
	}
	if output := execute("router"); output != "structured" {
		t.Fatalf("capability routing used the wrong provider: %q", output)
	}
	if structuredCalls.Load() != 1 || fastCalls.Load() != 1 {
		t.Fatalf("provider calls were not isolated: structured=%d fast=%d", structuredCalls.Load(), fastCalls.Load())
	}
}

func TestRunKeepsFirstSelectedModelRoute(t *testing.T) {
	preferred := modelRouteBinding(t, "preferred", "preferred-model", 200, modelrouting.Capabilities{})
	streaming := modelRouteBinding(t, "streaming", "streaming-model", 100, modelrouting.Capabilities{Streaming: true})
	preferred.Descriptor.MaxOutputTokens = 500
	streaming.Descriptor.MaxOutputTokens = 500
	catalog, err := modelrouting.NewCatalog(preferred, streaming)
	if err != nil {
		t.Fatal(err)
	}
	store := fixturestore.New()
	conversation, _ := store.CreateConversation("route affinity")
	runtime := NewRuntime(RuntimeOptions{
		Store: store, EmbeddingClient: newLocalFallbackOpenAIClientForTest(), ModelRoutes: catalog,
		ContextAssembly: domain.ContextAssemblyConfig{ContextWindowTokens: 1000, OutputReserveTokens: 100},
	})
	snapshot, err := runtime.captureRuntimeSnapshot(ChatModeSingle, domain.Agent{ID: "agent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.selectModelRoute(context.Background(), turnpkg.Request{
		RunID: run.ID, ConversationID: conversation.ID, Role: "answer", ModelMode: turnpkg.ModelModeText,
		Sink: runtime.runEventSink(),
	}, &snapshot)
	if err != nil || first.Route.ID != "preferred" {
		t.Fatalf("first route: decision=%#v err=%v", first, err)
	}
	second, err := runtime.selectModelRoute(context.Background(), turnpkg.Request{
		RunID: run.ID, ConversationID: conversation.ID, Role: "answer", ModelMode: turnpkg.ModelModeAgentStream,
		Sink: runtime.runEventSink(),
	}, &snapshot)
	if !errors.Is(err, modelrouting.ErrNoCompatibleRoute) || second.Route.ID != "" {
		t.Fatalf("run silently switched model routes: decision=%#v err=%v", second, err)
	}
	client, err := runtime.ModelClientForRun(run.ID)
	if err != nil || client.RuntimeIdentity().Model != "preferred-model" {
		t.Fatalf("resolve selected route: model=%#v err=%v", client, err)
	}
}

func modelRouteBinding(t *testing.T, id string, model string, priority int, capabilities modelrouting.Capabilities) modelrouting.Binding {
	t.Helper()
	client := openai.NewClient("", "https://models.test/v1", model)
	identity := client.RuntimeIdentity()
	return modelrouting.Binding{Descriptor: modelrouting.Descriptor{
		ID: id, Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
		Capabilities: capabilities, ContextWindowTokens: 1000, MaxOutputTokens: 100,
		Priority: priority, Pricing: modelrouting.Pricing{Source: "test_fixture"},
		CredentialEnvironment: "TEST_MODEL_API_KEY",
	}, Client: client}
}
