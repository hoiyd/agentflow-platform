package domain

import (
	"fmt"
	"slices"
)

type SkillInvocationPolicy string

const (
	SkillAutomatic    SkillInvocationPolicy = "automatic"
	SkillExplicitOnly SkillInvocationPolicy = "explicit_only"
)

// Missing policy retains the existing automatic behavior; restrictions combine
// by intersection and never grant executable capabilities.
func EffectiveSkillPolicy(packagePolicy, bindingPolicy SkillInvocationPolicy) SkillInvocationPolicy {
	if packagePolicy == SkillExplicitOnly || bindingPolicy == SkillExplicitOnly {
		return SkillExplicitOnly
	}
	return SkillAutomatic
}

func ValidateSkillPolicies(names []string, policies map[string]SkillInvocationPolicy) error {
	for name, policy := range policies {
		if !slices.Contains(names, name) || policy != SkillAutomatic && policy != SkillExplicitOnly {
			return fmt.Errorf("invalid invocation policy for bound Skill %q", name)
		}
	}
	return nil
}

type SkillToolDependency struct {
	Name                 string `json:"name"`
	Ready                bool   `json:"ready"`
	Reason               string `json:"reason,omitempty"`
	AgentBindingRequired bool   `json:"agent_binding_required"`
}

// SkillMetadata advertises a trusted method, not executable authority.
type SkillMetadata struct {
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Hash             string                `json:"hash"`
	RequiredTools    []string              `json:"required_tools,omitempty"`
	InvocationPolicy SkillInvocationPolicy `json:"invocation_policy,omitempty"`
	ToolDependencies []SkillToolDependency `json:"tool_dependencies,omitempty"`
}

// SkillSnapshot contains bounded text frozen with a Run. Resume never reads
// operator directories again; paths identify resources within this package.
type SkillSnapshot struct {
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Hash             string                `json:"hash"`
	RequiredTools    []string              `json:"required_tools,omitempty"`
	Instructions     string                `json:"instructions"`
	Resources        []SkillResource       `json:"resources,omitempty"`
	InvocationPolicy SkillInvocationPolicy `json:"invocation_policy,omitempty"`
	// Derived per-Agent view only; package identity must not change on binding.
	BindingPolicy SkillInvocationPolicy `json:"-"`
}

type SkillResource struct {
	Path    string `json:"path"`
	Hash    string `json:"hash"`
	Content string `json:"content"`
}

type SkillResourcePage struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Hash       string `json:"hash"`
	Content    string `json:"content"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
	TotalBytes int    `json:"total_bytes"`
	Truncated  bool   `json:"truncated"`
}
