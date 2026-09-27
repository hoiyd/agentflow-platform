package store

import (
	"testing"
	"time"
)

func TestDefaultResearchAgentCanSearch(t *testing.T) {
	for _, agent := range DefaultAgents(time.Now()) {
		if agent.ID != "agent_research" {
			continue
		}
		found := false
		for _, name := range agent.Tools {
			if name == "web_search" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("default research agent has no web_search tool")
		}
		return
	}
	t.Fatal("default research agent is missing")
}
