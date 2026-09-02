package toolpolicy

import (
	"fmt"
	"sort"
	"strings"
)

const CurrentVersion = "tool-security-policy-v1"

type Source string

const (
	SourceLocal  Source = "local"
	SourceRemote Source = "remote"
)

type ResourceKind string

const (
	ResourceRun          ResourceKind = "run"
	ResourceConversation ResourceKind = "conversation"
	ResourceWorkspace    ResourceKind = "workspace"
	ResourceFilesystem   ResourceKind = "filesystem"
	ResourceExternal     ResourceKind = "external_service"
)

type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

type ResourceScope struct {
	Kind   ResourceKind `json:"kind"`
	Name   string       `json:"name"`
	Access Access       `json:"access"`
}

type NetworkMode string

const (
	NetworkNone     NetworkMode = "none"
	NetworkInternal NetworkMode = "internal"
	NetworkExternal NetworkMode = "external"
)

type NetworkScope struct {
	Mode    NetworkMode `json:"mode"`
	Targets []string    `json:"targets,omitempty"`
}

type Scope struct {
	Resources   []ResourceScope `json:"resources,omitempty"`
	Network     NetworkScope    `json:"network"`
	Credentials []string        `json:"credential_scopes,omitempty"`
}

type SideEffectClass string

const (
	SideEffectNone          SideEffectClass = "none"
	SideEffectInternalWrite SideEffectClass = "internal_write"
	SideEffectExternalWrite SideEffectClass = "external_write"
	SideEffectDestructive   SideEffectClass = "destructive"
)

type RateClass string

const (
	// RateBounded means the shared Run Budget remains the enforcement owner.
	RateBounded  RateClass = "run_budgeted"
	RateElevated RateClass = "elevated"
)

type Reversibility string

const (
	Reversible    Reversibility = "reversible"
	Compensatable Reversibility = "compensatable"
	Irreversible  Reversibility = "irreversible"
)

type Visibility string

const (
	VisibilityRun      Visibility = "run"
	VisibilityUser     Visibility = "user"
	VisibilityOperator Visibility = "operator"
)

type ApprovalMode string

const (
	ApprovalNone      ApprovalMode = "none"
	ApprovalAsk       ApprovalMode = "ask"
	ApprovalHumanOnly ApprovalMode = "human_only"
)

type AuditLevel string

const (
	AuditBasic AuditLevel = "basic"
	AuditFull  AuditLevel = "full"
)

// Capability is the trusted local declaration of a Tool's maximum authority.
// Scope, Rate, Reversibility, and Visibility are explicit and orthogonal to
// timeout, concurrency, and the aggregate Run Budget.
type Capability struct {
	Source        Source          `json:"source"`
	Scope         Scope           `json:"scope"`
	SideEffect    SideEffectClass `json:"side_effect_class"`
	Rate          RateClass       `json:"rate"`
	Reversibility Reversibility   `json:"reversibility"`
	Visibility    Visibility      `json:"visibility"`
	Approval      ApprovalMode    `json:"approval_mode"`
	Audit         AuditLevel      `json:"audit_level"`
}

type Action string

const (
	ActionAllow       Action = "allow"
	ActionAllowAndLog Action = "allow_and_log"
	ActionAsk         Action = "ask"
	ActionDeny        Action = "deny"
	ActionHumanOnly   Action = "human_only"
)

// Rule is operator-owned. Capability is the maximum authority granted to the
// named Tool; model output and remote annotations never create or widen rules.
type Rule struct {
	ID         string     `json:"id"`
	Tool       string     `json:"tool"`
	Action     Action     `json:"action"`
	Capability Capability `json:"capability"`
}

type Policy struct {
	Version       string `json:"version"`
	DefaultAction Action `json:"default_action"`
	Rules         []Rule `json:"rules,omitempty"`
}

type Request struct {
	Tool                      string
	Declared                  Capability
	RequestedScope            Scope
	AvailableCredentialScopes []string
}

type Decision struct {
	Action        Action
	Allowed       bool
	PolicyVersion string
	RuleID        string
	Reason        string
	Capability    Capability
}

func (d Decision) AuditRequired() bool { return d.Allowed && d.Action == ActionAllowAndLog }

func DefaultPolicy() Policy {
	return Policy{
		Version: CurrentVersion, DefaultAction: ActionAllow,
		Rules: []Rule{{
			ID: "builtin-task-state-write", Tool: "update_task_state", Action: ActionAllowAndLog,
			Capability: NormalizeCapability(Capability{
				Scope: Scope{Resources: []ResourceScope{{
					Kind: ResourceConversation, Name: "task_state", Access: AccessWrite,
				}}},
				SideEffect: SideEffectInternalWrite, Reversibility: Compensatable,
				Visibility: VisibilityUser, Audit: AuditFull,
			}),
		}},
	}
}

func NormalizeCapability(capability Capability) Capability {
	if capability.Source == "" {
		capability.Source = SourceLocal
	}
	if capability.Scope.Network.Mode == "" {
		capability.Scope.Network.Mode = NetworkNone
	}
	if capability.SideEffect == "" {
		capability.SideEffect = SideEffectNone
	}
	if capability.Rate == "" {
		capability.Rate = RateBounded
	}
	if capability.Reversibility == "" {
		capability.Reversibility = Reversible
	}
	if capability.Visibility == "" {
		capability.Visibility = VisibilityRun
	}
	if capability.Approval == "" {
		capability.Approval = ApprovalNone
	}
	if capability.Audit == "" {
		capability.Audit = AuditBasic
	}
	capability.Scope = NormalizeScope(capability.Scope)
	return capability
}

func NormalizeScope(scope Scope) Scope {
	resources := append([]ResourceScope(nil), scope.Resources...)
	for index := range resources {
		resources[index].Name = strings.TrimSpace(resources[index].Name)
	}
	sort.Slice(resources, func(i, j int) bool {
		left, right := resources[i], resources[j]
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Name != right.Name {
			return left.Name < right.Name
		}
		return left.Access < right.Access
	})
	scope.Resources = deduplicateResources(resources)
	scope.Network.Targets = normalizeStrings(scope.Network.Targets)
	scope.Credentials = normalizeStrings(scope.Credentials)
	if scope.Network.Mode == "" {
		scope.Network.Mode = NetworkNone
	}
	return scope
}

func NormalizePolicy(policy Policy) Policy {
	policy.Version = strings.TrimSpace(policy.Version)
	if policy.Version == "" {
		policy.Version = CurrentVersion
	}
	if policy.DefaultAction == "" {
		policy.DefaultAction = ActionDeny
	}
	policy.Rules = append([]Rule(nil), policy.Rules...)
	for index := range policy.Rules {
		policy.Rules[index].ID = strings.TrimSpace(policy.Rules[index].ID)
		policy.Rules[index].Tool = strings.TrimSpace(policy.Rules[index].Tool)
		policy.Rules[index].Capability = NormalizeCapability(policy.Rules[index].Capability)
	}
	sort.Slice(policy.Rules, func(i, j int) bool { return policy.Rules[i].ID < policy.Rules[j].ID })
	return policy
}

func ValidateCapability(capability Capability) error {
	capability = NormalizeCapability(capability)
	if !oneOf(capability.Source, SourceLocal, SourceRemote) {
		return fmt.Errorf("unsupported Tool source %q", capability.Source)
	}
	if !oneOf(capability.SideEffect, SideEffectNone, SideEffectInternalWrite, SideEffectExternalWrite, SideEffectDestructive) {
		return fmt.Errorf("unsupported side-effect class %q", capability.SideEffect)
	}
	if !oneOf(capability.Rate, RateBounded, RateElevated) {
		return fmt.Errorf("unsupported rate class %q", capability.Rate)
	}
	if !oneOf(capability.Reversibility, Reversible, Compensatable, Irreversible) {
		return fmt.Errorf("unsupported reversibility %q", capability.Reversibility)
	}
	if !oneOf(capability.Visibility, VisibilityRun, VisibilityUser, VisibilityOperator) {
		return fmt.Errorf("unsupported visibility %q", capability.Visibility)
	}
	if !oneOf(capability.Approval, ApprovalNone, ApprovalAsk, ApprovalHumanOnly) {
		return fmt.Errorf("unsupported approval mode %q", capability.Approval)
	}
	if !oneOf(capability.Audit, AuditBasic, AuditFull) {
		return fmt.Errorf("unsupported audit level %q", capability.Audit)
	}
	return ValidateScope(capability.Scope)
}

func ValidateScope(scope Scope) error {
	scope = NormalizeScope(scope)
	if !oneOf(scope.Network.Mode, NetworkNone, NetworkInternal, NetworkExternal) {
		return fmt.Errorf("unsupported network mode %q", scope.Network.Mode)
	}
	if scope.Network.Mode == NetworkNone && len(scope.Network.Targets) > 0 {
		return fmt.Errorf("network targets require internal or external network mode")
	}
	if scope.Network.Mode != NetworkNone && len(scope.Network.Targets) == 0 {
		return fmt.Errorf("network mode %q requires explicit targets", scope.Network.Mode)
	}
	for _, resource := range scope.Resources {
		if !oneOf(resource.Kind, ResourceRun, ResourceConversation, ResourceWorkspace, ResourceFilesystem, ResourceExternal) {
			return fmt.Errorf("unsupported resource kind %q", resource.Kind)
		}
		if strings.TrimSpace(resource.Name) == "" {
			return fmt.Errorf("resource scope name is required")
		}
		if !oneOf(resource.Access, AccessRead, AccessWrite) {
			return fmt.Errorf("unsupported resource access %q", resource.Access)
		}
	}
	return nil
}

func ValidatePolicy(policy Policy) error {
	if strings.TrimSpace(policy.Version) == "" {
		return fmt.Errorf("Tool security policy version is required")
	}
	policy = NormalizePolicy(policy)
	if !validAction(policy.DefaultAction) {
		return fmt.Errorf("unsupported default action %q", policy.DefaultAction)
	}
	seenIDs, seenTools := map[string]bool{}, map[string]bool{}
	for _, rule := range policy.Rules {
		if rule.ID == "" || rule.Tool == "" {
			return fmt.Errorf("Tool security rule ID and Tool are required")
		}
		if seenIDs[rule.ID] || seenTools[rule.Tool] {
			return fmt.Errorf("Tool security rules require unique IDs and Tool names")
		}
		seenIDs[rule.ID], seenTools[rule.Tool] = true, true
		if !validAction(rule.Action) {
			return fmt.Errorf("rule %q has unsupported action %q", rule.ID, rule.Action)
		}
		if err := ValidateCapability(rule.Capability); err != nil {
			return fmt.Errorf("rule %q capability: %w", rule.ID, err)
		}
	}
	return nil
}

// Evaluate is deterministic and fail closed. It never receives argument text or
// credential values, only locally resolved scope identifiers.
func Evaluate(policy Policy, request Request) Decision {
	policy = NormalizePolicy(policy)
	declared := NormalizeCapability(request.Declared)
	requested := declared
	requested.Scope = NormalizeScope(request.RequestedScope)
	decision := Decision{Action: ActionDeny, PolicyVersion: policy.Version, Reason: "policy_invalid", Capability: requested}
	if err := ValidatePolicy(policy); err != nil {
		return decision
	}
	if strings.TrimSpace(request.Tool) == "" || ValidateCapability(declared) != nil || ValidateScope(requested.Scope) != nil {
		decision.Reason = "capability_invalid"
		return decision
	}
	if !scopeContains(declared.Scope, requested.Scope) {
		decision.Reason = "binding_scope_expansion"
		return decision
	}
	if !containsStrings(normalizeStrings(request.AvailableCredentialScopes), requested.Scope.Credentials) {
		decision.Reason = "credential_scope_unavailable"
		return decision
	}

	for _, rule := range policy.Rules {
		if rule.Tool != strings.TrimSpace(request.Tool) {
			continue
		}
		decision.RuleID = rule.ID
		if !capabilityContains(rule.Capability, requested) {
			decision.Reason = "rule_scope_mismatch"
			return decision
		}
		return decisionForAction(decision, rule.Action, requested)
	}

	if declared.Source == SourceRemote {
		decision.Reason = "remote_tool_requires_rule"
		return decision
	}
	if sensitive(requested) {
		decision.Reason = "sensitive_scope_requires_rule"
		return decision
	}
	return decisionForAction(decision, policy.DefaultAction, requested)
}

func decisionForAction(decision Decision, action Action, capability Capability) Decision {
	decision.Action = action
	switch action {
	case ActionAllow, ActionAllowAndLog:
		if capability.Approval != ApprovalNone {
			decision.Action, decision.Reason = ActionDeny, "approval_policy_not_satisfied"
			return decision
		}
		if capability.Reversibility == Irreversible && action != ActionAllowAndLog {
			decision.Action, decision.Reason = ActionDeny, "irreversible_action_requires_logged_authorization"
			return decision
		}
		decision.Allowed, decision.Reason = true, "policy_allowed"
	case ActionAsk, ActionHumanOnly:
		decision.Reason = "human_approval_required"
	default:
		decision.Action, decision.Reason = ActionDeny, "policy_denied"
	}
	return decision
}

func capabilityContains(maximum, requested Capability) bool {
	maximum, requested = NormalizeCapability(maximum), NormalizeCapability(requested)
	return maximum.Source == requested.Source && maximum.SideEffect == requested.SideEffect &&
		maximum.Rate == requested.Rate && maximum.Reversibility == requested.Reversibility &&
		maximum.Visibility == requested.Visibility && maximum.Approval == requested.Approval &&
		maximum.Audit == requested.Audit && scopeContains(maximum.Scope, requested.Scope)
}

func scopeContains(maximum, requested Scope) bool {
	maximum, requested = NormalizeScope(maximum), NormalizeScope(requested)
	if maximum.Network.Mode != requested.Network.Mode || !containsStrings(maximum.Network.Targets, requested.Network.Targets) ||
		!containsStrings(maximum.Credentials, requested.Credentials) {
		return false
	}
	available := make(map[ResourceScope]bool, len(maximum.Resources))
	for _, resource := range maximum.Resources {
		available[resource] = true
	}
	for _, resource := range requested.Resources {
		if !available[resource] {
			return false
		}
	}
	return true
}

func sensitive(capability Capability) bool {
	if capability.SideEffect != SideEffectNone || capability.Scope.Network.Mode != NetworkNone ||
		len(capability.Scope.Credentials) > 0 || capability.Rate != RateBounded {
		return true
	}
	for _, resource := range capability.Scope.Resources {
		if resource.Access == AccessWrite || resource.Kind == ResourceFilesystem || resource.Kind == ResourceExternal {
			return true
		}
	}
	return false
}

func normalizeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return deduplicateStrings(result)
}

func deduplicateStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func deduplicateResources(values []ResourceScope) []ResourceScope {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func containsStrings(maximum, requested []string) bool {
	available := make(map[string]bool, len(maximum))
	for _, value := range maximum {
		available[value] = true
	}
	for _, value := range requested {
		if !available[value] {
			return false
		}
	}
	return true
}

func validAction(action Action) bool {
	return oneOf(action, ActionAllow, ActionAllowAndLog, ActionAsk, ActionDeny, ActionHumanOnly)
}

func oneOf[T comparable](value T, allowed ...T) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
