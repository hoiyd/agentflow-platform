package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

func TestConversationHandoffPreview(t *testing.T) {
	const workspace = "workspace-handoff"
	storage := fixturestore.New()
	conversation, err := storage.CreateConversationInWorkspace(workspace, "Source")
	if err != nil {
		t.Fatal(err)
	}
	handler := (&Handler{store: storage}).Routes()
	path := "/api/conversations/" + conversation.ID + "/handoff"
	read := func(scope string, status int) (string, bool) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(WorkspaceHeader, scope)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("preview response %d, want %d: %s", res.Code, status, res.Body)
		}
		if status != http.StatusOK {
			return "", false
		}
		var payload struct {
			Draft         string `json:"draft"`
			MaxCharacters int    `json:"max_characters"`
			Truncated     bool   `json:"truncated"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil || payload.MaxCharacters != 4000 || utf8.RuneCountInString(payload.Draft) > payload.MaxCharacters {
			t.Fatalf("invalid bounded draft: %+v %v", payload, err)
		}
		return payload.Draft, payload.Truncated
	}
	if draft, _ := read(workspace, http.StatusOK); draft != "" {
		t.Fatalf("empty source should allow a manual draft, got %q", draft)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = storage.CommitContextCompaction(domain.ContextCompaction{
		ID: "cmp-handoff", ConversationID: conversation.ID, RunID: run.ID, Generation: 2,
		Summary: "DO NOT COPY raw legacy summary or approvals", SourceHash: "handoff-source",
	}, domain.RunEvent{Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": "cmp-handoff", "trigger": "hard", "status": "completed", "algorithm_version": "fixture"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = storage.ForWorkspace(domain.NewWorkspaceScope(workspace)).ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{
		Operations: []domain.TaskStateOperation{
			{Type: "set_goal", Goal: "Release evidence"},
			{Type: "upsert_constraint", Constraint: &domain.TaskConstraint{ID: "scope", Statement: "Staging only"}},
			{Type: "upsert_task", Task: &domain.TaskItem{ID: "open", Title: "Verify rollback", Status: domain.TaskItemPending}},
			{Type: "upsert_task", Task: &domain.TaskItem{ID: "done", Title: "Exclude completed task", Status: domain.TaskItemCompleted}},
		},
	}, domain.TaskStateSource{ActorType: "user"})
	if err != nil {
		t.Fatal(err)
	}
	draft, truncated := read(workspace, http.StatusOK)
	if truncated {
		t.Fatal("short handoff was truncated")
	}
	for _, required := range []string{"Release evidence", "Staging only", "Verify rollback", "/workspace?conversation=" + conversation.ID, "/runs/" + run.ID} {
		if !strings.Contains(draft, required) {
			t.Errorf("missing %q in %q", required, draft)
		}
	}
	for _, excluded := range []string{"DO NOT COPY", "Exclude completed task"} {
		if strings.Contains(draft, excluded) {
			t.Errorf("unexpected %q in %q", excluded, draft)
		}
	}
	read("workspace-other", http.StatusNotFound)

	var operations []domain.TaskStateOperation
	for i := range 5 {
		operations = append(operations, domain.TaskStateOperation{Type: "upsert_constraint",
			Constraint: &domain.TaskConstraint{ID: "long-" + strconv.Itoa(i), Statement: strings.Repeat("\u6c34", 900)}})
	}
	_, err = storage.ForWorkspace(domain.NewWorkspaceScope(workspace)).ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{
		ExpectedVersion: 1, Operations: operations,
	}, domain.TaskStateSource{ActorType: "user"})
	if err != nil {
		t.Fatal(err)
	}
	draft, truncated = read(workspace, http.StatusOK)
	if !truncated || !strings.HasSuffix(draft, "/runs/"+run.ID) || !utf8.ValidString(draft) {
		t.Fatal("large draft should keep a valid, bounded body and intact source references")
	}
}
