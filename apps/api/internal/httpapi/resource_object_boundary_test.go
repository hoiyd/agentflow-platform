package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/capture"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/store"
)

func TestAuthenticatedResourceObjectBoundaries(t *testing.T) {
	handler, storage, db, spaces, tokens := authorizationFixture(t)
	conversation, err := storage.CreateConversationInWorkspace(spaces[0], "Private conversation")
	if err != nil {
		t.Fatal(err)
	}
	message, err := storage.AddMessage(conversation.ID, "user", "Private source")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{{Type: domain.TaskStateSetGoal, Goal: "Private task"}}}, domain.TaskStateSource{ActorType: "user"}); err != nil {
		t.Fatal(err)
	}
	memory, err := handler.memories.Commit(t.Context(), domain.Memory{WorkspaceID: spaces[0], Kind: "note", Content: "Private memory", ConversationID: conversation.ID, RunID: run.ID, SourceMessageID: message.ID})
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("Private artifact needle")
	expires := time.Now().Add(time.Hour)
	artifact, err := storage.CreateToolArtifact(domain.ToolArtifact{ID: "artifact-authorization", SchemaVersion: domain.CurrentToolArtifactSchemaVersion, RunID: run.ID, ToolCallID: "call-artifact", ToolName: "future_tool", MediaType: "text/plain", ContentHash: store.ToolArtifactContentHash(content), OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: time.Now(), ExpiresAt: &expires}, content)
	if err != nil {
		t.Fatal(err)
	}
	effect, _, err := storage.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "effect-authorization", RunID: run.ID, StageID: "worker", ToolCallID: "call-effect", ToolName: "future_writer", DefinitionRevision: "fixture", RequestHash: "request-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "Private uncertain write"); err != nil {
		t.Fatal(err)
	}
	recorder := capture.NewRecorder(storage, capture.Options{Mode: domain.ModelRequestCaptureFull})
	ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: conversation.ID})
	if err := recorder.Record(ctx, requestcontrol.Observation{ModelCallID: "private-call", Operation: "chat.completion", Provider: "fixture", Model: "test", Payload: []byte(`{"model":"test","messages":[{"role":"user","content":"Private captured request"}]}`)}); err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()
	for _, path := range []string{
		"/api/conversations/" + conversation.ID + "/messages",
		"/api/conversations/" + conversation.ID + "/task-state",
		"/api/conversations/" + conversation.ID + "/task-state/revisions",
		"/api/conversations/" + conversation.ID + "/task-state/revisions/1",
		"/api/memories/" + memory.ID,
		"/api/runs/" + run.ID,
		"/api/runs/" + run.ID + "/replay",
		"/api/runs/" + run.ID + "/projection",
		"/api/runs/" + run.ID + "/events",
		"/api/runs/" + run.ID + "/model_requests?include_content=true",
		"/api/runs/" + run.ID + "/artifacts",
		"/api/runs/" + run.ID + "/artifacts/" + artifact.ID,
		"/api/runs/" + run.ID + "/artifacts/" + artifact.ID + "/search?q=needle",
		"/api/runs/" + run.ID + "/tool-effects",
		"/api/runs/" + run.ID + "/episode",
		"/api/runs/" + run.ID + "/collaboration_steps",
	} {
		t.Run(path, func(t *testing.T) {
			owned := authorizedRequest(routes, "GET", path, spaces[0], tokens[0], "")
			if owned.Code != 200 {
				t.Fatalf("owner cannot read: %d %s", owned.Code, owned.Body.String())
			}
			for _, scope := range []struct{ workspace, token string }{{spaces[1], tokens[0]}, {spaces[2], tokens[1]}, {spaces[0], tokens[1]}} {
				denied := authorizedRequest(routes, "GET", path, scope.workspace, scope.token, "")
				if denied.Code != 404 || strings.Contains(denied.Body.String(), "Private") {
					t.Fatalf("cross-scope read: %d %s", denied.Code, denied.Body.String())
				}
			}
		})
	}
	for _, command := range []struct{ method, path, body string }{
		{"PATCH", "/api/conversations/" + conversation.ID, `{"title":"Unauthorized"}`},
		{"DELETE", "/api/conversations/" + conversation.ID, ""},
		{"POST", "/api/chat", `{"conversation_id":"` + conversation.ID + `","message":"Unauthorized","mode":"single"}`},
		{"PATCH", "/api/conversations/" + conversation.ID + "/task-state", `{"expected_version":1,"operations":[{"type":"set_goal","goal":"Unauthorized"}]}`},
		{"POST", "/api/memories/" + memory.ID + "/mutations", `{"operation_id":"unauthorized","expected_version":1,"action":"delete","actor":"owner","reason":"Unauthorized"}`},
		{"POST", "/api/runs/" + run.ID + "/cancel", ""},
		{"POST", "/api/runs/" + run.ID + "/resume", `{}`},
		{"POST", "/api/runs/" + run.ID + "/continue", `{"plan":"Unauthorized"}`},
		{"POST", "/api/runs/" + run.ID + "/verify", `{}`},
		{"POST", "/api/runs/" + run.ID + "/tool-effects/" + effect.IdempotencyKey + "/reconcile", `{"command_id":"unauthorized","action":"confirm_failed","expected_version":2,"actor":"owner","reason":"Unauthorized"}`},
	} {
		t.Run(command.method+command.path, func(t *testing.T) {
			response := authorizedRequest(routes, command.method, command.path, spaces[2], tokens[1], command.body)
			if response.Code != 404 {
				t.Fatalf("unauthorized command: %d %s", response.Code, response.Body.String())
			}
		})
	}
	// Parent identity is part of the lookup, not merely a UI nesting convention.
	sibling, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/runs/" + sibling.ID + "/artifacts/" + artifact.ID, "/api/runs/" + sibling.ID + "/artifacts/" + artifact.ID + "/search?q=needle"} {
		if response := authorizedRequest(routes, "GET", path, spaces[0], tokens[0], ""); response.Code != 404 {
			t.Fatalf("wrong-parent artifact: %d %s", response.Code, response.Body.String())
		}
	}
	response := authorizedRequest(routes, "POST", "/api/runs/"+sibling.ID+"/tool-effects/"+effect.IdempotencyKey+"/reconcile", spaces[0], tokens[0], `{"command_id":"wrong-parent","action":"confirm_failed","expected_version":2,"actor":"owner","reason":"checked"}`)
	if response.Code != 404 {
		t.Fatalf("wrong-parent effect: %d %s", response.Code, response.Body.String())
	}
	var state string
	if err := db.QueryRow(`SELECT state->>'goal' FROM task_state_revisions WHERE conversation_id=$1 ORDER BY version DESC LIMIT 1`, conversation.ID).Scan(&state); err != nil || state != "Private task" {
		t.Fatalf("unauthorized task write: %q %v", state, err)
	}
	detail, err := storage.GetMemoryDetail(spaces[0], memory.ID)
	if err != nil || detail.Memory.DeletedAt != nil || detail.Memory.Version != 1 {
		t.Fatalf("unauthorized memory write: %#v %v", detail, err)
	}
	effects, err := storage.ListToolEffects(run.ID)
	if err != nil || len(effects) != 1 || effects[0].Version != 2 {
		t.Fatalf("unauthorized effect write: %#v %v", effects, err)
	}
	var revisions int
	if err := db.QueryRow(`SELECT count(*) FROM task_state_revisions WHERE conversation_id=$1`, conversation.ID).Scan(&revisions); err != nil || revisions != 1 {
		t.Fatalf("unexpected revision: %d %v", revisions, err)
	}
	for _, suffix := range []string{"", "/attention"} {
		response := authorizedRequest(routes, "GET", "/api/runs"+suffix, spaces[2], tokens[1], "")
		if response.Code != 200 || strings.Contains(response.Body.String(), run.ID) {
			t.Fatalf("list leaked run: %d %s", response.Code, response.Body.String())
		}
	}
	evidence, _ := json.Marshal(map[string]any{"store": "disposable-postgres", "owners": 2, "workspaces": 3, "checks": []string{"owner-positive", "foreign-object", "same-owner-other-workspace", "wrong-parent-artifact/effect", "mutations-preserve-state", "capture-content-isolation"}})
	t.Log("resource_authorization_evidence=" + string(evidence))
}
