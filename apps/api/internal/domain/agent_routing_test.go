package domain

import "testing"

func TestNormalizeAgentRoutingRequirementsAndDetectConflicts(t *testing.T) {
	requirements := NormalizeAgentRoutingRequirements(AgentRoutingRequirements{
		RequiredTools:         []string{" calculator ", "CALCULATOR"},
		ProhibitedTools:       []string{"Calculator"},
		PreferredCapabilities: []string{" research ", "RESEARCH", ""},
	})
	if len(requirements.RequiredTools) != 1 || len(requirements.PreferredCapabilities) != 1 {
		t.Fatalf("requirements were not normalized: %#v", requirements)
	}
	conflicts := requirements.ConflictingTools()
	if len(conflicts) != 1 || conflicts[0] != "calculator" || requirements.IsEmpty() {
		t.Fatalf("conflict detection failed: %#v", conflicts)
	}
}

func TestEmptyAgentRoutingRequirements(t *testing.T) {
	if !NormalizeAgentRoutingRequirements(AgentRoutingRequirements{}).IsEmpty() {
		t.Fatal("zero routing requirements must remain empty")
	}
}
