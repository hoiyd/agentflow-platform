package modelrouting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/redaction"
)

const PolicyRevision = "model-route-priority-v1"

const (
	ReasonToolCallingUnsupported      = "tool_calling_unsupported"
	ReasonStructuredOutputUnsupported = "structured_output_unsupported"
	ReasonStreamingUnsupported        = "streaming_unsupported"
	ReasonContextWindowExceeded       = "context_window_exceeded"
	ReasonOutputLimitExceeded         = "output_limit_exceeded"
)

var (
	ErrInvalidCatalog = failure.New(failure.Definition{
		Message: "model route catalog is invalid",
		Info:    failure.Info{Code: "model_route_catalog_invalid", Source: "model_router", Category: failure.CategoryValidation},
	})
	ErrInvalidRequirements = failure.New(failure.Definition{
		Message: "model route requirements are invalid",
		Info:    failure.Info{Code: "model_route_requirements_invalid", Source: "model_router", Category: failure.CategoryValidation},
	})
	ErrNoCompatibleRoute = failure.New(failure.Definition{
		Message: "no compatible model route is available",
		Info:    failure.Info{Code: "model_route_unavailable", Source: "model_router", Category: failure.CategoryAvailability},
	})
)

var (
	routeIDPattern       = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	credentialEnvPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

type Capabilities = domain.ModelRouteCapabilities
type Pricing = domain.ModelRoutePricing
type Descriptor = domain.ModelRouteDescriptor
type Requirements = domain.ModelRouteRequirements
type CandidateDecision = domain.ModelRouteCandidateDecision

type Binding struct {
	Descriptor Descriptor
	Client     modelprovider.Client
}

type Decision struct {
	PolicyRevision  string
	CatalogRevision string
	Requirements    Requirements
	Route           Descriptor
	Client          modelprovider.Client
	Candidates      []CandidateDecision
}

// Catalog is immutable after construction, so concurrent model calls observe
// one deterministic set of route contracts.
type Catalog struct {
	bindings []Binding
	byID     map[string]Binding
	revision string
}

func NewCatalog(bindings ...Binding) (*Catalog, error) {
	if len(bindings) == 0 {
		return nil, errors.Join(ErrInvalidCatalog, errors.New("at least one model route is required"))
	}
	normalized := make([]Binding, 0, len(bindings))
	seen := make(map[string]bool, len(bindings))
	for _, binding := range bindings {
		descriptor, err := ValidateDescriptor(binding.Descriptor)
		if err != nil {
			return nil, err
		}
		if binding.Client == nil {
			return nil, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q has no client", descriptor.ID))
		}
		identity := binding.Client.RuntimeIdentity()
		if identity.Provider != descriptor.Provider || identity.BaseURL != descriptor.Endpoint || identity.Model != descriptor.Model {
			return nil, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q descriptor does not match its client", descriptor.ID))
		}
		if seen[descriptor.ID] {
			return nil, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q is duplicated", descriptor.ID))
		}
		seen[descriptor.ID] = true
		binding.Descriptor = descriptor
		normalized = append(normalized, binding)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i].Descriptor.ID < normalized[j].Descriptor.ID })
	descriptors := make([]Descriptor, 0, len(normalized))
	for _, binding := range normalized {
		descriptors = append(descriptors, binding.Descriptor)
	}
	revision, err := CatalogRevision(descriptors)
	if err != nil {
		return nil, errors.Join(ErrInvalidCatalog, err)
	}
	catalog := &Catalog{bindings: normalized, byID: make(map[string]Binding, len(normalized)), revision: revision}
	for _, binding := range normalized {
		catalog.byID[binding.Descriptor.ID] = binding
	}
	return catalog, nil
}

func (c *Catalog) Revision() string {
	if c == nil {
		return ""
	}
	return c.revision
}

func (c *Catalog) Descriptors() []Descriptor {
	if c == nil {
		return nil
	}
	result := make([]Descriptor, 0, len(c.bindings))
	for _, binding := range c.bindings {
		result = append(result, binding.Descriptor)
	}
	return result
}

func (c *Catalog) Resolve(id string) (Binding, bool) {
	if c == nil {
		return Binding{}, false
	}
	binding, ok := c.byID[id]
	return binding, ok
}

func (c *Catalog) Select(requirements Requirements) (Decision, error) {
	return c.selectRoute("", requirements)
}

// SelectRoute validates a previously chosen route against the current Turn.
// It preserves Run-level model affinity without weakening capability checks.
func (c *Catalog) SelectRoute(routeID string, requirements Requirements) (Decision, error) {
	return c.selectRoute(strings.TrimSpace(routeID), requirements)
}

func (c *Catalog) selectRoute(routeID string, requirements Requirements) (Decision, error) {
	requirements.Purpose = strings.TrimSpace(requirements.Purpose)
	if c == nil || requirements.Purpose == "" || requirements.EstimatedInputTokens < 0 || requirements.MaxOutputTokens <= 0 {
		return Decision{Requirements: requirements}, ErrInvalidRequirements
	}
	decision := Decision{
		PolicyRevision: PolicyRevision, CatalogRevision: c.revision, Requirements: requirements,
		Candidates: make([]CandidateDecision, 0, len(c.bindings)),
	}
	eligible := make([]Binding, 0, len(c.bindings))
	for _, binding := range c.bindings {
		reasons := exclusionReasons(binding.Descriptor, requirements)
		if routeID != "" && binding.Descriptor.ID != routeID {
			reasons = append(reasons, "run_route_affinity")
		}
		decision.Candidates = append(decision.Candidates, CandidateDecision{
			RouteID: binding.Descriptor.ID, Eligible: len(reasons) == 0, ExclusionReasons: reasons,
		})
		if len(reasons) == 0 {
			eligible = append(eligible, binding)
		}
	}
	if len(eligible) == 0 {
		return decision, ErrNoCompatibleRoute
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].Descriptor.Priority != eligible[j].Descriptor.Priority {
			return eligible[i].Descriptor.Priority > eligible[j].Descriptor.Priority
		}
		return eligible[i].Descriptor.ID < eligible[j].Descriptor.ID
	})
	decision.Route = eligible[0].Descriptor
	decision.Client = eligible[0].Client
	return decision, nil
}

func (c *Catalog) HasConfiguredClient() bool {
	if c == nil {
		return false
	}
	for _, binding := range c.bindings {
		if binding.Client.HasAPIKey() {
			return true
		}
	}
	return false
}

func ValidateDescriptor(descriptor Descriptor) (Descriptor, error) {
	descriptor.ID = strings.TrimSpace(descriptor.ID)
	descriptor.Provider = strings.TrimSpace(descriptor.Provider)
	descriptor.Model = strings.TrimSpace(descriptor.Model)
	descriptor.Endpoint = strings.TrimSpace(descriptor.Endpoint)
	descriptor.Pricing.Source = strings.TrimSpace(descriptor.Pricing.Source)
	descriptor.CredentialEnvironment = strings.TrimSpace(descriptor.CredentialEnvironment)
	if !routeIDPattern.MatchString(descriptor.ID) {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, errors.New("model route id must be a stable lowercase identifier"))
	}
	if descriptor.Provider == "" || descriptor.Model == "" || descriptor.Pricing.Source == "" {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q has incomplete metadata", descriptor.ID))
	}
	endpoint, err := url.Parse(descriptor.Endpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q has an invalid endpoint", descriptor.ID))
	}
	if descriptor.ContextWindowTokens <= 0 || descriptor.MaxOutputTokens <= 0 || descriptor.MaxOutputTokens > descriptor.ContextWindowTokens {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q has invalid token limits", descriptor.ID))
	}
	if descriptor.Pricing.InputPerMillionTokensMicros < 0 || descriptor.Pricing.OutputPerMillionTokensMicros < 0 {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q has invalid pricing", descriptor.ID))
	}
	if descriptor.CredentialEnvironment != "" && !credentialEnvPattern.MatchString(descriptor.CredentialEnvironment) {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q has an invalid credential environment reference", descriptor.ID))
	}
	if err := redaction.ValidateText(descriptor.Provider, descriptor.Model, descriptor.Endpoint, descriptor.Pricing.Source, descriptor.CredentialEnvironment); err != nil {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q contains credential-like metadata", descriptor.ID))
	}
	revision, err := descriptorRevision(descriptor)
	if err != nil {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, err)
	}
	if descriptor.DefinitionRevision != "" && descriptor.DefinitionRevision != revision {
		return Descriptor{}, errors.Join(ErrInvalidCatalog, fmt.Errorf("model route %q definition revision does not match", descriptor.ID))
	}
	descriptor.DefinitionRevision = revision
	return descriptor, nil
}

func exclusionReasons(route Descriptor, requirements Requirements) []string {
	reasons := make([]string, 0, 5)
	if requirements.ToolCalling && !route.Capabilities.ToolCalling {
		reasons = append(reasons, ReasonToolCallingUnsupported)
	}
	if requirements.StructuredOutput && !route.Capabilities.StructuredOutput {
		reasons = append(reasons, ReasonStructuredOutputUnsupported)
	}
	if requirements.Streaming && !route.Capabilities.Streaming {
		reasons = append(reasons, ReasonStreamingUnsupported)
	}
	if requirements.MaxOutputTokens > route.MaxOutputTokens {
		reasons = append(reasons, ReasonOutputLimitExceeded)
	}
	if requirements.EstimatedInputTokens+requirements.MaxOutputTokens > route.ContextWindowTokens {
		reasons = append(reasons, ReasonContextWindowExceeded)
	}
	return reasons
}

func descriptorRevision(descriptor Descriptor) (string, error) {
	descriptor.DefinitionRevision = ""
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func CatalogRevision(descriptors []Descriptor) (string, error) {
	descriptors = append([]Descriptor(nil), descriptors...)
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].ID < descriptors[j].ID })
	encoded, err := json.Marshal(descriptors)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
