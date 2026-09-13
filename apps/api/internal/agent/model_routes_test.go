package agent

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelrouting"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/tools"
	turnpkg "agentflow-platform/apps/api/internal/turn"
)

func TestFrozenModelCatalogIgnoresLaterRoutesAndRetainsIdentity(t *testing.T) {
	original := modelRouteBinding(t, "primary", "model-v1", 100, modelrouting.Capabilities{Streaming: true})
	originalCatalog, err := modelrouting.NewCatalog(original)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{
		ModelClient: original.Client, ModelRoutes: originalCatalog,
		ContextAssembly: domain.ContextAssemblyConfig{ContextWindowTokens: 1000, OutputReserveTokens: 100},
	})
	snapshot, err := runtime.captureRuntimeSnapshot(ChatModeSingle, domain.Agent{ID: "agent"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	changed := modelRouteBinding(t, "primary", "model-v2", 100, modelrouting.Capabilities{Streaming: true})
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
	if len(descriptors) != 1 || descriptors[0].ID != "primary" || descriptors[0].Model != "model-v1" {
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
		ModelClient: binding.Client, ModelRoutes: catalog,
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
