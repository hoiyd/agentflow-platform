package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

// Read-boundary inventory: absent/out-of-scope/uncontracted Runs return 404;
// provisional results return 409 without value; each persistence failure returns
// 500 rather than an empty success. Reading an old Run must not return a newer answer.
func TestRunResultReadsRealCompletionEvidenceAndFailsClosed(t *testing.T) {
	dependencies := completeHandlerDependencies(t)
	storage := fullStoreForTest(t, dependencies)
	registry := verification.NewRegistry(verification.Options{})
	contract, err := registry.FreezeContract(&domain.CompletionContract{Output: &domain.StructuredOutputConfig{Mode: "local", VerifierID: "report"}, Verifiers: []domain.VerifierSpec{{ID: "report", Type: domain.VerifierJSONSchema, Required: true, Config: map[string]any{"schema": map[string]any{"type": "object"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversation("report evidence")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storage.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	read := func(storage HTTPStore, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/runs/"+id+"/result", nil)
		r.SetPathValue("id", id)
		r.Header.Set("X-Workspace-ID", run.WorkspaceID)
		w := httptest.NewRecorder()
		(&Handler{store: storage}).getRunResult(w, r)
		return w
	}
	if response := read(dependencies.Store, run.ID); response.Code != 409 {
		t.Fatalf("pending result: %d %s", response.Code, response.Body.String())
	}
	scoped := storage.ForWorkspace(domain.NewWorkspaceScope(run.WorkspaceID))
	chunk, err := runcompletion.Complete(context.Background(), scoped, runcompletion.Dependencies{Runtime: dependencies.AgentRuntime, Verification: dependencies.Verification}, runcompletion.Request{RunID: run.ID, ConversationID: conversation.ID, Assistant: `{"summary":"original"}`})
	if err != nil || chunk.ResultURL != "/api/runs/"+run.ID+"/result" {
		t.Fatalf("complete: %+v %v", chunk, err)
	}
	if _, err = storage.AddMessage(conversation.ID, "assistant", `{"summary":"different run"}`); err != nil {
		t.Fatal(err)
	}
	response := read(dependencies.Store, run.ID)
	var result domain.StructuredRunResult
	if err = json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || string(result.Value) != `{"summary":"original"}` || result.EvidenceID == "" {
		t.Fatalf("wrong machine result: %d %s", response.Code, response.Body.String())
	}
	if response = read(dependencies.Store, "missing"); response.Code != 404 {
		t.Fatalf("missing run: %d", response.Code)
	}
	ordinary, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if response = read(dependencies.Store, ordinary.ID); response.Code != 404 {
		t.Fatalf("ordinary chat exposed result: %d", response.Code)
	}
	quality := store.CloneCompletionContract(contract)
	quality.Verifiers = append(quality.Verifiers, domain.VerifierSpec{ID: "quality", Type: domain.VerifierTextConstraints, Required: true, Config: map[string]any{"min_characters": 1000}})
	quality.Policy.MaxAttempts, quality.Policy.OnExhausted = 3, domain.VerificationFailRun
	quality, err = registry.FreezeContract(quality)
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), quality)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = storage.UpdateRunStatus(rejected.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	_, err = runcompletion.Complete(context.Background(), scoped, runcompletion.Dependencies{Runtime: dependencies.AgentRuntime, Verification: dependencies.Verification}, runcompletion.Request{RunID: rejected.ID, ConversationID: conversation.ID, Assistant: `{"summary":"valid JSON is not quality evidence"}`})
	if err != nil {
		t.Fatal(err)
	}
	response = read(dependencies.Store, rejected.ID)
	if response.Code != 409 {
		t.Fatalf("schema-only success bypassed other verifiers: %d %s", response.Code, response.Body.String())
	}
	var unavailable map[string]any
	if err = json.Unmarshal(response.Body.Bytes(), &unavailable); err != nil {
		t.Fatal(err)
	}
	if _, exposed := unavailable["value"]; exposed || unavailable["verification_status"] != "failed" {
		t.Fatalf("failed gate exposed JSON: %v", unavailable)
	}
	handler := &Handler{store: dependencies.Store, agentRuntime: dependencies.AgentRuntime, verification: dependencies.Verification}
	reverify := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/runs/"+rejected.ID+"/verify", nil)
		r.SetPathValue("id", rejected.ID)
		r.Header.Set("X-Workspace-ID", rejected.WorkspaceID)
		w := httptest.NewRecorder()
		handler.verifyRun(w, r)
		return w
	}
	if response = reverify(); response.Code != 200 {
		t.Fatalf("original candidate could not be reverified: %s", response.Body.String())
	}
	if _, err = storage.AddMessage(conversation.ID, "user", "a new task"); err != nil {
		t.Fatal(err)
	}
	if response = reverify(); response.Code != 409 {
		t.Fatalf("question drift accepted as original subject: %d", response.Code)
	}
	if _, err = storage.AddMessage(conversation.ID, "assistant", `{"summary":"`+strings.Repeat("later report ", 100)+`"}`); err != nil {
		t.Fatal(err)
	}
	if response = reverify(); response.Code != 409 {
		t.Fatalf("later answer reused as old Run candidate: %d %s", response.Code, response.Body.String())
	}
	for _, point := range []string{"run", "evidence", "artifacts"} {
		response = read(resultFailureStore{Store: storage, point: point}, run.ID)
		if response.Code != 500 {
			t.Fatalf("%s failure returned %d", point, response.Code)
		}
	}
}

type resultFailureStore struct {
	store.Store
	point string
}

func (s resultFailureStore) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	return resultFailureScope{s.Store.ForWorkspace(scope), s.point}
}

type resultFailureScope struct {
	store.WorkspaceStore
	point string
}

func (s resultFailureScope) GetRun(id string) (domain.Run, bool, error) {
	if s.point == "run" {
		return domain.Run{}, false, errors.New("result storage unavailable")
	}
	return s.WorkspaceStore.GetRun(id)
}
func (s resultFailureScope) ListVerificationEvidence(id string) ([]domain.VerificationEvidence, error) {
	if s.point == "evidence" {
		return nil, errors.New("evidence storage unavailable")
	}
	return s.WorkspaceStore.ListVerificationEvidence(id)
}
func (s resultFailureScope) ListVerificationArtifacts(id string) ([]domain.VerificationArtifact, error) {
	if s.point == "artifacts" {
		return nil, errors.New("artifact storage unavailable")
	}
	return s.WorkspaceStore.ListVerificationArtifacts(id)
}
