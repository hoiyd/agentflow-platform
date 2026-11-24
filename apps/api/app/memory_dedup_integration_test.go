package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestProductionExactMemoryDeduplication(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("TAVILY_API_KEY", "")
	model := httptest.NewServer(http.HandlerFunc(newBrowserProvider().respond))
	defer model.Close()
	cfg := durableTestConfig(t, root, database, routing.RouteConfig{ID: "memory-dedup-fixture", BaseURL: model.URL, RequestTimeoutSeconds: 10})
	app, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := closeDurableTestApplication(app); err != nil {
			t.Error(err)
		}
	}()
	server := httptest.NewServer(app)
	defer server.Close()

	const fact = "The release report is due on Monday."
	var agent domain.Agent
	durableTestRequest(t, server, "POST", "/api/agents", domain.Agent{
		Name: "Memory dedup fixture", SystemPrompt: "Answer concisely.", MemoryEnabled: true,
	}, 201, &agent)
	var other domain.Workspace
	durableTestRequest(t, server, "POST", "/api/workspaces", map[string]string{"name": "Other Memory workspace"}, 201, &other)
	vector := make([]float64, 1536)
	vector[0] = 1
	for _, item := range []domain.Memory{
		{WorkspaceID: other.ID, Kind: "fact", Content: fact},
		{WorkspaceID: agent.WorkspaceID, UserID: "scoped-user", Kind: "fact", Content: fact},
		{WorkspaceID: agent.WorkspaceID, ProjectID: "scoped-project", Kind: "fact", Content: fact},
		{WorkspaceID: agent.WorkspaceID, Kind: "note", Content: fact},
	} {
		if _, err := app.store.CreateMemory(item, domain.MemoryEmbedding{Provider: "openai_compatible", Model: cfg.EmbeddingModel, Embedding: vector}); err != nil {
			t.Fatal(err)
		}
	}

	// Every chat starts a new Conversation, so stable same-source IDs alone cannot pass this test.
	save := func(message string) (domain.MemoryCandidate, string) {
		t.Helper()
		stream := durableTestRequest(t, server, "POST", "/api/chat", domain.ChatRequest{
			AgentID: agent.ID, Mode: "single", Message: message,
		}, 200, nil)
		done := durableTestDone(t, stream)
		runID, _ := done["run_id"].(string)
		if runID == "" || done["status"] != "completed" {
			t.Fatalf("chat did not complete: %s", stream)
		}
		run, ok, err := app.store.GetRun(runID)
		if err != nil || !ok {
			t.Fatalf("load run: %t %v", ok, err)
		}
		timeout := time.NewTimer(8 * time.Second)
		defer timeout.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			events, err := app.store.ListRunEvents(runID)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Type == domain.EventMemorySyncFailed {
					t.Fatalf("memory sync failed: %+v", event.Payload)
				}
				if event.Type != domain.EventMemorySyncCompleted {
					continue
				}
				candidates, err := app.store.ListMemoryCandidates(run.ConversationID)
				id, _ := event.Payload["memory_id"].(string)
				if err != nil || len(candidates) != 1 || candidates[0].Status != domain.MemoryCandidateAccepted ||
					candidates[0].ID != event.Payload["candidate_id"] || id == "" {
					t.Fatalf("missing candidate/Memory receipt: %+v %v, event=%+v", candidates, err, event.Payload)
				}
				return candidates[0], id
			}
			select {
			case <-tick.C:
			case <-timeout.C:
				t.Fatalf("Run %s did not finish Memory sync", runID)
			}
		}
	}
	first, firstID := save("Remember that " + fact)
	if firstID != "mem_curated_"+first.ID {
		t.Fatalf("matched a different scope or kind: %s", firstID)
	}
	duplicate, duplicateID := save("Please remember that  " + fact + "  ")
	if duplicate.SourceMessageID == first.SourceMessageID || duplicate.ID == first.ID || duplicate.Content != first.Content {
		t.Fatalf("duplicate did not preserve distinct candidate audit: %+v %+v", first, duplicate)
	}
	if duplicateID != firstID {
		t.Errorf("same exact fact created another Memory: got %s, want %s", duplicateID, firstID)
	}
	nearby, nearbyID := save("Remember that The release report is due on Tuesday.")
	if nearbyID != "mem_curated_"+nearby.ID {
		t.Fatalf("different fact was merged: %s", nearbyID)
	}

	durableTestRequest(t, server, "POST", "/api/memories/"+firstID+"/mutations", domain.MemoryMutation{
		OperationID: "delete-before-recall", ExpectedVersion: 1, Action: "delete",
		Actor: "operator", Reason: "fixture tombstone",
	}, 200, nil)
	afterDelete, afterDeleteID := save("Remember that " + fact)
	if afterDeleteID != "mem_curated_"+afterDelete.ID {
		t.Errorf("fresh source reused old Memory after deletion: %s", afterDeleteID)
	}
	var removed domain.MemoryDetail
	durableTestRequest(t, server, "GET", "/api/memories/"+firstID, nil, 200, &removed)
	if removed.Memory.DeletedAt == nil || removed.Memory.Version != 2 {
		t.Fatalf("original record was resurrected: %+v", removed.Memory)
	}

	var manual domain.Memory
	durableTestRequest(t, server, "POST", "/api/memories", domain.Memory{Kind: "fact", Content: fact}, 201, &manual)
	if manual.ID == afterDeleteID || manual.SourceMessageID != "" {
		t.Fatalf("manual write was deduplicated: %+v", manual)
	}
	t.Logf("exact Memory dedup: fixture=memory-dedup-fixture first=%s duplicate=%s near=%s after_delete=%s manual=%s; production HTTP, pgvector, synthetic provider", firstID, duplicateID, nearbyID, afterDeleteID, manual.ID)
}
