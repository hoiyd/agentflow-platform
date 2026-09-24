package routing

import (
	"errors"
	"math"
	"slices"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/openai"
)

func TestCatalogSelectsStableCompatibleRoute(t *testing.T) {
	primary := testBinding("primary", 10, Capabilities{ToolCalling: true, StructuredOutput: true, Streaming: true}, 1000, 100)
	limited := testBinding("limited", 100, Capabilities{}, 100, 20)
	catalog, err := NewCatalog(limited, primary)
	if err != nil {
		t.Fatal(err)
	}

	decision, err := catalog.Select(Requirements{
		Purpose: "worker", ToolCalling: true, StructuredOutput: true, Streaming: true,
		EstimatedInputTokens: 90, MaxOutputTokens: 30,
	})
	if err != nil || decision.Route.ID != "primary" {
		t.Fatalf("expected compatible route, decision=%#v err=%v", decision, err)
	}
	if got := decision.Candidates[0]; got.RouteID != "limited" || got.Eligible || !slices.Equal(got.ExclusionReasons, []string{
		ReasonToolCallingUnsupported, ReasonStructuredOutputUnsupported, ReasonStreamingUnsupported,
		ReasonOutputLimitExceeded, ReasonContextWindowExceeded,
	}) {
		t.Fatalf("unexpected exclusion evidence: %#v", got)
	}

	alpha := testBinding("alpha", 50, primary.Descriptor.Capabilities, 1000, 100)
	beta := testBinding("beta", 50, primary.Descriptor.Capabilities, 1000, 100)
	tied, err := NewCatalog(beta, alpha)
	if err != nil {
		t.Fatal(err)
	}
	tieDecision, err := tied.Select(Requirements{Purpose: "primary", MaxOutputTokens: 10})
	if err != nil || tieDecision.Route.ID != "alpha" {
		t.Fatalf("route ID must break priority ties: %#v err=%v", tieDecision, err)
	}
}

func TestCatalogFreezesEffectiveSamplingAndRejectsUnsupportedValues(t *testing.T) {
	binding := testBinding("primary", 1, Capabilities{Streaming: true}, 1000, 100)
	defaultCatalog, err := NewCatalog(binding)
	if err != nil {
		t.Fatal(err)
	}
	defaultRoute := defaultCatalog.Descriptors()[0]
	if defaultRoute.GenerationPolicy == nil || *defaultRoute.GenerationPolicy.AnswerStream.Temperature != 0.4 ||
		*defaultRoute.GenerationPolicy.Completion.Temperature != 0.2 || defaultRoute.GenerationPolicy.AnswerStream.TopP != nil {
		t.Fatalf("unexpected effective defaults: %#v", defaultRoute.GenerationPolicy)
	}
	policy := domain.DefaultGenerationPolicy()
	zero := 0.0
	policy.AnswerStream.Temperature = &zero
	binding.Descriptor.GenerationPolicy = &policy
	changed, err := NewCatalog(binding)
	if err != nil || changed.Revision() == defaultCatalog.Revision() {
		t.Fatalf("sampling changes did not change experiment identity: revision=%q err=%v", changed.Revision(), err)
	}
	seed := int64(42)
	policy.AnswerStream.Seed = &seed
	binding.Descriptor.GenerationPolicy = &policy
	if _, err := NewCatalog(binding); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("unsupported seed reached a route: %v", err)
	}
	binding.Descriptor.Capabilities.Seed = true
	policy.AnswerStream.TopP = &zero
	if _, err := NewCatalog(binding); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("invalid top_p reached a route: %v", err)
	}
	policy.AnswerStream.TopP = nil
	invalid := math.NaN()
	policy.AnswerStream.Temperature = &invalid
	if _, err := NewCatalog(binding); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("non-finite temperature reached a route: %v", err)
	}
}

func TestCatalogReturnsTypedNoCompatibleRoute(t *testing.T) {
	catalog, err := NewCatalog(testBinding("text_only", 1, Capabilities{}, 100, 20))
	if err != nil {
		t.Fatal(err)
	}
	decision, err := catalog.Select(Requirements{Purpose: "router", StructuredOutput: true, MaxOutputTokens: 10})
	if !errors.Is(err, ErrNoCompatibleRoute) || failure.Describe(err).Code != "model_route_unavailable" {
		t.Fatalf("expected typed no-route error, got %v", err)
	}
	if len(decision.Candidates) != 1 || !slices.Contains(decision.Candidates[0].ExclusionReasons, ReasonStructuredOutputUnsupported) {
		t.Fatalf("missing no-route evidence: %#v", decision)
	}
	if _, err := catalog.Select(Requirements{MaxOutputTokens: 10}); !errors.Is(err, ErrInvalidRequirements) {
		t.Fatalf("expected invalid requirements error, got %v", err)
	}
}

func TestCatalogRevisionsAreDeterministicAndSecretFree(t *testing.T) {
	alpha := testBinding("alpha", 1, Capabilities{Streaming: true}, 1000, 100)
	beta := testBinding("beta", 2, Capabilities{Streaming: true}, 1000, 100)
	first, err := NewCatalog(alpha, beta)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCatalog(beta, alpha)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision() != second.Revision() || first.Descriptors()[0].DefinitionRevision == "" {
		t.Fatalf("unstable catalog revision: %q %q", first.Revision(), second.Revision())
	}
	if resolved, ok := first.Resolve("alpha"); !ok || resolved.Descriptor.ID != "alpha" {
		t.Fatalf("registered route cannot be resolved: %#v ok=%v", resolved, ok)
	}

	frozen := first.Descriptors()[0]
	if _, err := ValidateDescriptor(frozen); err != nil {
		t.Fatalf("valid frozen descriptor rejected: %v", err)
	}
	frozen.Priority++
	if _, err := ValidateDescriptor(frozen); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("tampered descriptor revision was accepted: %v", err)
	}

	secret := alpha
	secret.Descriptor.Endpoint = "https://example.test/v1?token=private"
	if _, err := NewCatalog(secret); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("credential-like endpoint was accepted: %v", err)
	}
}

func TestCatalogRejectsInvalidBindings(t *testing.T) {
	valid := testBinding("primary", 1, Capabilities{Streaming: true}, 1000, 100)
	tests := []struct {
		name     string
		bindings []Binding
	}{
		{name: "empty"},
		{name: "duplicate", bindings: []Binding{valid, valid}},
		{name: "invalid id", bindings: []Binding{mutateBinding(valid, func(value *Descriptor) { value.ID = "Not Stable" })}},
		{name: "missing metadata", bindings: []Binding{mutateBinding(valid, func(value *Descriptor) { value.Pricing.Source = "" })}},
		{name: "missing client", bindings: []Binding{{Descriptor: valid.Descriptor}}},
		{name: "invalid endpoint", bindings: []Binding{mutateBinding(valid, func(value *Descriptor) { value.Endpoint = "localhost" })}},
		{name: "invalid limits", bindings: []Binding{mutateBinding(valid, func(value *Descriptor) { value.MaxOutputTokens = 1001 })}},
		{name: "invalid pricing", bindings: []Binding{mutateBinding(valid, func(value *Descriptor) { value.Pricing.InputPerMillionTokensMicros = -1 })}},
		{name: "invalid credential reference", bindings: []Binding{mutateBinding(valid, func(value *Descriptor) { value.CredentialEnvironment = "model-key" })}},
		{name: "client mismatch", bindings: []Binding{mutateBinding(valid, func(value *Descriptor) { value.Model = "other-model" })}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCatalog(test.bindings...); !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("invalid binding accepted: %v", err)
			}
		})
	}
}

func testBinding(id string, priority int, capabilities Capabilities, contextWindow int, maxOutput int) Binding {
	client := openai.NewClient("", "https://models.test/v1", "test-model")
	identity := client.RuntimeIdentity()
	return Binding{Descriptor: Descriptor{
		ID: id, Provider: identity.Provider, Model: identity.Model, Endpoint: identity.BaseURL,
		Capabilities: capabilities, ContextWindowTokens: contextWindow, MaxOutputTokens: maxOutput,
		Priority: priority, Pricing: Pricing{Source: "test_fixture"},
	}, Client: client}
}

func mutateBinding(binding Binding, mutate func(*Descriptor)) Binding {
	mutate(&binding.Descriptor)
	binding.Descriptor.DefinitionRevision = ""
	return binding
}
