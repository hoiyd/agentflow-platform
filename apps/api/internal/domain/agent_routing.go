package domain

import "strings"

// AgentRoutingRequirements are user-approved constraints for one Multi-Agent
// delegation. Hard fields restrict eligibility; PreferredCapabilities only
// influence ranking and never grant tools, retrieval, or other authority.
type AgentRoutingRequirements struct {
	RequiredTools         []string `json:"required_tools,omitempty"`
	ProhibitedTools       []string `json:"prohibited_tools,omitempty"`
	RequireMemory         bool     `json:"require_memory,omitempty"`
	RequireRetrieval      bool     `json:"require_retrieval,omitempty"`
	PreferredCapabilities []string `json:"preferred_capabilities,omitempty"`
}

func NormalizeAgentRoutingRequirements(requirements AgentRoutingRequirements) AgentRoutingRequirements {
	requirements.RequiredTools = normalizeUniqueStrings(requirements.RequiredTools)
	requirements.ProhibitedTools = normalizeUniqueStrings(requirements.ProhibitedTools)
	requirements.PreferredCapabilities = normalizeUniqueStrings(requirements.PreferredCapabilities)
	return requirements
}

func (r AgentRoutingRequirements) ConflictingTools() []string {
	prohibited := make(map[string]bool, len(r.ProhibitedTools))
	for _, name := range r.ProhibitedTools {
		prohibited[strings.ToLower(strings.TrimSpace(name))] = true
	}
	conflicts := make([]string, 0)
	for _, name := range r.RequiredTools {
		if prohibited[strings.ToLower(strings.TrimSpace(name))] {
			conflicts = append(conflicts, name)
		}
	}
	return conflicts
}

func (r AgentRoutingRequirements) IsEmpty() bool {
	return len(r.RequiredTools) == 0 && len(r.ProhibitedTools) == 0 &&
		!r.RequireMemory && !r.RequireRetrieval && len(r.PreferredCapabilities) == 0
}
