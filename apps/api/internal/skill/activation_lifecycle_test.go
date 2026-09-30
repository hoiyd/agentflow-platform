package skill

import (
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestExplicitSkillManifestRetainsFrozenMethodAndRereadsPages(t *testing.T) {
	dir := packageDirectory(t, "bounded", "name: bounded\ndescription: Evidence method", "ORIGINAL_METHOD")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	packages, err := catalog.Freeze([]string{"bounded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Agent: domain.RuntimeAgentSnapshot{ID: "agent", Skills: []string{"bounded"}}, CandidateAgents: []domain.RuntimeAgentSnapshot{{ID: "other", Skills: []string{"bounded"}}}, Skills: packages}
	storage := fixturestore.New()
	conversation, err := storage.CreateConversation("explicit lifecycle")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: "original", TurnID: "original"}), "agent", "/skill:bounded Begin")
	session := contextassembly.Session{AgentID: "agent", CurrentInput: "/skill:bounded Begin", Config: contextassembly.DefaultConfig(), Sink: eventpkg.StoreSink{Store: storage}, LoadSkills: func() ([]domain.SkillSnapshot, error) { return NewService(storage).Active(ctx, &snapshot) }}
	if _, err := contextassembly.Assemble(contextassembly.WithSession(ctx, session), contextassembly.Request{Model: "fixture", Messages: []contextassembly.Message{{Role: "system", Content: "Use facts."}, {Role: "user", Content: "Begin"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	// Resume/new Stage has no explicit prefix and no resource history or Tool load.
	resumed := WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: "resumed", TurnID: "resumed"}), "agent", "Continue")
	service := NewService(storage)
	active, err := service.Active(resumed, &snapshot)
	if err != nil || len(active) != 1 || active[0].Hash != packages[0].Hash {
		t.Fatalf("resumed=%+v err=%v", active, err)
	}
	page, err := service.read(resumed, json.RawMessage(`{"name":"bounded","path":"references/checklist.md","limit":12}`))
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.read(resumed, json.RawMessage(`{"name":"bounded","path":"references/checklist.md","limit":12}`))
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(page)
	right, _ := json.Marshal(again)
	if string(left) != string(right) {
		t.Fatal("frozen page changed after history trimming")
	}
	if other, err := service.Active(WithAgent(resumed, "other", "Continue"), &snapshot); err != nil || len(other) != 0 {
		t.Fatalf("other inherited method=%+v err=%v", other, err)
	}
	canceled, cancel := context.WithCancel(resumed)
	cancel()
	if _, err := service.read(canceled, json.RawMessage(`{"name":"bounded","path":"references/checklist.md"}`)); err != context.Canceled {
		t.Fatal("canceled read did not stop", err)
	}
}

func TestExplicitSkillManifestRejectsIncompleteOrForeignEvidence(t *testing.T) {
	dir := packageDirectory(t, "bounded", "name: bounded\ndescription: Method", "body")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	packages, err := catalog.Freeze([]string{"bounded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Agent: domain.RuntimeAgentSnapshot{ID: "agent", Skills: []string{"bounded"}}, Skills: packages}
	for _, test := range []struct {
		name   string
		mutate func(*domain.ContextManifest)
	}{
		{"foreign Run", func(m *domain.ContextManifest) { m.RunID = "foreign" }},
		{"foreign Agent", func(m *domain.ContextManifest) { m.AgentID = "foreign" }},
		{"wrong hash", func(m *domain.ContextManifest) { m.Entries[0].ReferenceID = "skill:bounded@wrong" }},
		{"metadata", func(m *domain.ContextManifest) { m.Entries[0].Source = "skill_metadata" }},
		{"unselected", func(m *domain.ContextManifest) { m.Entries[0].Selected = false }},
		{"unknown activation", func(m *domain.ContextManifest) { m.Entries[0].Activation = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			storage := fixturestore.New()
			conversation, err := storage.CreateConversation("evidence")
			if err != nil {
				t.Fatal(err)
			}
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			manifest := domain.ContextManifest{RunID: run.ID, AgentID: "agent", Entries: []domain.ContextManifestEntry{{Source: "skill_instructions", ReferenceID: "skill:bounded@" + packages[0].Hash, Selected: true, Activation: "explicit"}}}
			test.mutate(&manifest)
			if _, err := storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventContextAssembled, Payload: map[string]any{"manifest": manifest}}); err != nil {
				t.Fatal(err)
			}
			ctx := WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID}), "agent", "")
			if active, err := NewService(storage).Active(ctx, &snapshot); err != nil || len(active) != 0 {
				t.Fatalf("accepted incomplete evidence=%+v err=%v", active, err)
			}
		})
	}
}
