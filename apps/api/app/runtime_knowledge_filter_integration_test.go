package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Real production HTTP/commands/Runtime/Bindings/Postgres. Only model and
// embedding transport are controlled. Failure inventory: runtime-knowledge-filters.md.
func TestProductionRuntimeKnowledgeFilter(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	fixture := newBrowserProvider()
	var revokeFinal atomic.Bool
	var withdrawDuringResume atomic.Bool
	var withdraw func() error
	withdrawResult := make(chan error, 1)
	var revoke func() error
	revokeResult := make(chan error, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded := httptest.NewRecorder()
		fixture.respond(recorded, r)
		if strings.HasSuffix(r.URL.Path, "/chat/completions") && withdrawDuringResume.CompareAndSwap(true, false) {
			withdrawResult <- withdraw()
		}
		if revokeFinal.Load() && strings.Contains(recorded.Body.String(), "Scope evidence is sufficient") && !strings.Contains(recorded.Body.String(), "tool_calls") && revokeFinal.CompareAndSwap(true, false) {
			revokeResult <- revoke()
		}
		for key, values := range recorded.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorded.Code)
		_, _ = w.Write(recorded.Body.Bytes())
	}))
	defer provider.Close()
	t.Setenv("BROWSER_FIXTURE_KEY", "fixture-only")
	t.Setenv("EMBEDDING_API_KEY", "fixture-only")
	t.Setenv("TAVILY_API_KEY", "")
	routes, _ := json.Marshal(map[string]any{"routes": []any{map[string]any{"id": "scope-fixture", "model": "fixture-model", "base_url": provider.URL, "credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 20, "context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100, "capabilities": map[string]bool{"tool_calling": true, "streaming": true, "structured_output": true}, "pricing": map[string]string{"source": "fixture"}}}})
	route := filepath.Join(root, "routes.json")
	if err := os.WriteFile(route, routes, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	cfg.DatabaseURL, cfg.ModelRouteConfigPath, cfg.ToolConfigPath = pgfixture.DatabaseURL(t), route, filepath.Join(root, "tools.json")
	cfg.AuthMode, cfg.TrustedSkillDirectories, cfg.SandboxEnabled = "local", "", false
	cfg.EmbeddingBaseURL, cfg.EmbeddingModel, cfg.EmbeddingDimensions = provider.URL, "fixture-embedding", 1536
	cfg.ContextCompactionMode, cfg.MemoryAdaptiveExtractionMode, cfg.RerankerMode = "off", "off", "heuristic"
	cfg.RouterMode, cfg.AutonomousMaxIterations, cfg.ModelRetryMaxAttempts, cfg.ToolSchemaMode = "auto", 1, 1, "eager"
	cfg.ModelRequestsPerMinute, cfg.ModelTokensPerMinute, cfg.RunMaxModelCalls = 0, 0, 32
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	s, err := store.NewPostgresStore(cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	workspace, err := s.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	revoke = func() error {
		_, err := db.Exec(`DELETE FROM auth_memberships WHERE workspace_id=$1`, workspace)
		return err
	}
	owned, err := s.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "Scope reader", Description: "ROLLBACK-742 scoped recovery", RetrievalEnabled: true, Tools: []string{"knowledge_search", "knowledge_read"}})
	if err != nil {
		t.Fatal(err)
	}
	send := func(t *testing.T, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		data, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", path, bytes.NewReader(data))
		req.Header.Set("X-Workspace-ID", workspace)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.server.Handler.ServeHTTP(w, req)
		return w
	}
	call := func(t *testing.T, path string, body any, want int) []byte {
		t.Helper()
		w := send(t, path, body)
		if w.Code != want || want == 200 && strings.Contains(w.Body.String(), "event: error") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	var eligible domain.Document
	for _, product := range []string{"AgentFlow", "Other"} {
		text := "ROLLBACK-742 scoped recovery preserves durable state."
		if product == "Other" {
			text += " FORBIDDEN_SCOPE_CONTENT"
		}
		body := call(t, "/api/documents", map[string]any{"title": product, "content": text, "metadata_schema_version": "knowledge-metadata-v1", "metadata": map[string]any{"product": product, "priority": 9}}, 201)
		var doc domain.Document
		if json.Unmarshal(body, &doc) != nil {
			t.Fatal("ingest JSON")
		}
		if product == "AgentFlow" {
			eligible = doc
		}
	}
	var filter domain.MetadataFilter
	withdraw = func() error {
		current, _, ok, err := s.GetDocument(eligible.ID)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("fixture document missing")
		}
		_, err = s.UpdateDocumentMetadata(workspace, current.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: current.UpdatedAt, Metadata: current.Metadata, Lifecycle: &domain.DocumentLifecycle{Status: "withdrawn"}})
		return err
	}
	// Lifecycle restrictions sit outside the user expression. Matching labels,
	// identical query terms and recent ingestion must not admit inactive sources.
	for _, lifecycle := range []domain.DocumentLifecycle{{Status: "withdrawn"}, {Status: "draft"}, {ValidFrom: "9999-01-01T00:00:00Z"}, {ValidUntil: "2000-01-01T00:00:00Z"}} {
		call(t, "/api/documents", map[string]any{"title": "Inactive AgentFlow", "content": "ROLLBACK-742 scoped recovery preserves durable state. FORBIDDEN_SCOPE_CONTENT", "metadata_schema_version": "knowledge-metadata-v1", "metadata": map[string]any{"product": "AgentFlow", "priority": 9}, "lifecycle": lifecycle}, 201)
	}
	var autonomous domain.Run
	if err := json.Unmarshal([]byte(`{"schema_version":"knowledge-metadata-v1","where":{"all":[{"any":[{"field":"document.metadata.product","op":"eq","value":"AgentFlow"},{"field":"chunk.metadata.product","op":"eq","value":"Unmatched"}]},{"field":"chunk.metadata.priority","op":"lt","value":9.00000000000000001},{"field":"document.metadata.priority","op":"in","value":[9,9.0]},{"field":"chunk.metadata.priority","op":"gte","value":-0}]}}`), &filter); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			conv, err := s.CreateConversationInWorkspace(workspace, "Frozen scope "+mode)
			if err != nil {
				t.Fatal(err)
			}
			call(t, "/api/chat", map[string]any{"conversation_id": conv.ID, "agent_id": owned.ID, "mode": mode, "message": "rag-filter-gate ROLLBACK-742", "knowledge_filter": filter}, 200)
			runs, err := s.ListRunsByWorkspace(workspace)
			if err != nil {
				t.Fatal(err)
			}
			var run domain.Run
			for _, item := range runs {
				if item.ConversationID == conv.ID {
					run = item
				}
			}
			if run.ID == "" || run.RuntimeSnapshot == nil || run.RuntimeSnapshot.KnowledgeScope == nil {
				t.Fatal("Run lost frozen Knowledge scope")
			}
			frozenID := run.RuntimeSnapshot.KnowledgeScope.ID
			if mode == "multi_agent" {
				changed := owned
				changed.RetrievalEnabled = false
				if _, err := s.UpdateAgent(changed); err != nil {
					t.Fatal(err)
				}
				if err := a.Close(t.Context()); err != nil {
					t.Fatal(err)
				}
				a, err = New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				call(t, "/api/runs/"+run.ID+"/continue", map[string]any{"plan": "rag-filter-gate ROLLBACK-742"}, 200)
				if _, err := s.UpdateAgent(owned); err != nil {
					t.Fatal(err)
				}
			}
			run, _, err = s.GetRun(run.ID)
			if err != nil || run.Status != domain.RunCompleted {
				t.Fatalf("Run status=%s error=%s load=%v", run.Status, run.Error, err)
			}
			if _, err := eligibility.FrozenFilter(run.RuntimeSnapshot); err != nil {
				t.Fatal(err)
			}
			if mode == "autonomous" {
				autonomous = run
			}
			events, err := s.ListRunEvents(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			retrievals, searches, reads := 0, 0, 0
			for _, e := range events {
				if e.Type == domain.EventRetrievalCompleted && e.Payload["knowledge_scope"] != nil {
					retrievals++
					encoded, _ := json.Marshal(e.Payload["knowledge_scope"])
					if !strings.Contains(string(encoded), frozenID) {
						t.Fatal("trace identity drift")
					}
				}
				if e.Type == domain.EventToolCompleted && e.Payload["tool_name"] == "knowledge_search" {
					searches++
					data, _ := json.Marshal(e.Payload["result"])
					var result domain.KnowledgeToolSearchResult
					if json.Unmarshal(data, &result) != nil || len(result.Items) != 1 || result.Items[0].DocumentID != eligible.ID {
						t.Fatalf("Tool scope lost: %s", data)
					}
				}
				if e.Type == domain.EventToolCompleted && e.Payload["tool_name"] == "knowledge_read" {
					reads++
				}
			}
			if retrievals == 0 || searches == 0 || reads == 0 {
				t.Fatalf("missing real paths retrieval=%d search=%d read=%d", retrievals, searches, reads)
			}
			// Re-read labels at citation resolution, including immutable Tool pages.
			updated, err := s.UpdateDocumentMetadata(workspace, eligible.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: eligible.UpdatedAt, Metadata: map[string]any{"product": "Other", "priority": 9}})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := runcompletion.ResolveCitations(s.ForWorkspace(domain.NewWorkspaceScope(workspace)), run.ID, "Evidence [S1]"); err == nil {
				t.Fatal("stale citation escaped frozen eligibility")
			}
			eligible, err = s.UpdateDocumentMetadata(workspace, eligible.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: updated.UpdatedAt, Metadata: map[string]any{"product": "AgentFlow", "priority": 9}})
			if err != nil {
				t.Fatal(err)
			}
			updated, err = s.UpdateDocumentMetadata(workspace, eligible.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: eligible.UpdatedAt, Metadata: eligible.Metadata, Lifecycle: &domain.DocumentLifecycle{Status: "withdrawn"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := runcompletion.ResolveCitations(s.ForWorkspace(domain.NewWorkspaceScope(workspace)), run.ID, "Evidence [S1]"); !errors.Is(err, eligibility.ErrSourceUnavailable) {
				t.Fatalf("withdrawn citation accepted: %v", err)
			}
			eligible, err = s.UpdateDocumentMetadata(workspace, eligible.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: updated.UpdatedAt, Metadata: updated.Metadata, Lifecycle: &domain.DocumentLifecycle{}})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("runtime-filter-evidence mode=%s workspace=%s run=%s scope=%s retrievals=%d searches=%d reads=%d restart=%v stale_citation_rejected=true", mode, workspace, run.ID, frozenID, retrievals, searches, reads, mode == "multi_agent")
		})
	}
	// Dispatch a real durable follow-up; it must not default to an unfiltered Run.
	var queued domain.RunInput
	input := call(t, "/api/conversations/"+autonomous.ConversationID+"/inputs", domain.RunInputRequest{Kind: "follow_up", RunID: autonomous.ID, Content: "rag-filter-gate ROLLBACK-742", AgentID: owned.ID, Mode: "single", IdempotencyKey: "scope-followup"}, 202)
	if err := json.Unmarshal(input, &queued); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := s.ListRunInputs(wait, identity.SuperUserID, workspace, autonomous.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		finished := false
		for _, item := range items {
			if item.ID != queued.ID || item.AppliedRunID == "" {
				continue
			}
			run, _, err := s.GetRun(item.AppliedRunID)
			if err != nil || run.Status == domain.RunFailed {
				t.Fatalf("follow-up failed: %+v %v", run, err)
			}
			if run.Status == domain.RunCompleted {
				if run.RuntimeSnapshot.KnowledgeScope == nil || run.RuntimeSnapshot.KnowledgeScope.ID != autonomous.RuntimeSnapshot.KnowledgeScope.ID {
					t.Fatal("follow-up lost the originating scope")
				}
				finished = true
			}
		}
		if finished {
			break
		}
		select {
		case <-wait.Done():
			t.Fatal("follow-up did not finish within its bounded deadline")
		case <-ticker.C:
		}
	}
	// Seed only a durable crash boundary. All Resume validation, model requests,
	// retrieval, Tools and completion run through the production command path.
	var resumable domain.Run
	for _, corruption := range []string{"", "hash", "policy", "filter", "withdrawn", "withdraw_after_start"} {
		conv, err := s.CreateConversationInWorkspace(workspace, "Scope recovery")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.AddMessage(conv.ID, "user", "rag-filter-gate ROLLBACK-742"); err != nil {
			t.Fatal(err)
		}
		snapshot := store.CloneRuntimeSnapshot(*autonomous.RuntimeSnapshot)
		switch corruption {
		case "hash":
			snapshot.KnowledgeScope.ID = "sha256:corrupt"
		case "policy":
			snapshot.KnowledgeScope.PolicyVersion = "unsupported"
		case "filter":
			snapshot.KnowledgeScope.Filter.SchemaVersion = "unsupported"
		}
		run, err := s.CreateRunWithContract(owned.ID, conv.ID, *snapshot, nil)
		if err != nil {
			t.Fatal(err)
		}
		step, err := s.CreateCollaborationStep(domain.CollaborationStep{RunID: run.ID, ConversationID: conv.ID, AgentID: owned.ID, Role: "observe", Status: domain.CollaborationStepCompleted, Iteration: 0, Input: "User task: rag-filter-gate ROLLBACK-742\n\nCurrent state: none", Output: "Saved observation."})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := checkpoint.NewInternalProvider(s).RecordStageTransition(t.Context(), step, domain.EventStageCompleted); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateRunStatus(run.ID, domain.RunFailedRecoverable, "fixture crash boundary"); err != nil {
			t.Fatal(err)
		}
		if corruption == "withdrawn" || corruption == "withdraw_after_start" {
			_, chunks, _, err := s.GetDocument(eligible.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": []domain.RAGCitation{{SourceID: "S1", DocumentID: eligible.ID, DocumentVersion: eligible.Version, ChunkID: chunks[0].ID}}}}); err != nil {
				t.Fatal(err)
			}
			if corruption == "withdrawn" {
				if err := withdraw(); err != nil {
					t.Fatal(err)
				}
			} else {
				withdrawDuringResume.Store(true)
			}
		}
		if err := a.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		a, err = New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		response := send(t, "/api/runs/"+run.ID+"/resume", map[string]any{"user_input": ""})
		updated, _, loadErr := s.GetRun(run.ID)
		requests, recordErr := s.ListModelRequestRecords(run.ID)
		if loadErr != nil || recordErr != nil {
			t.Fatalf("Resume persistence: %v %v", loadErr, recordErr)
		}
		if corruption != "" {
			code := "invalid_metadata_filter"
			if corruption == "withdrawn" || corruption == "withdraw_after_start" {
				code = "knowledge_source_unavailable"
			}
			wantStatus, wantRequests := domain.RunFailedRecoverable, 0
			if corruption == "withdraw_after_start" {
				select {
				case err := <-withdrawResult:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("provider did not reach the withdrawal boundary")
				}
				wantStatus, wantRequests = domain.RunFailed, 1
			}
			if !strings.Contains(response.Body.String(), `"code":"`+code+`"`) || len(requests) != wantRequests || updated.Status != wantStatus {
				t.Fatalf("corrupt scope reached provider: requests=%d %s", len(requests), response.Body.String())
			}
		} else {
			if updated.Status != domain.RunCompleted || len(requests) == 0 || strings.Contains(response.Body.String(), "event: error") || updated.RuntimeSnapshot.KnowledgeScope.ID != autonomous.RuntimeSnapshot.KnowledgeScope.ID {
				t.Fatalf("Resume lost scope: status=%s requests=%d %s", updated.Status, len(requests), response.Body.String())
			}
			resumable = updated
		}
		if corruption == "withdrawn" || corruption == "withdraw_after_start" {
			eligible, _, _, err = s.GetDocument(eligible.ID)
			if err != nil {
				t.Fatal(err)
			}
			eligible, err = s.UpdateDocumentMetadata(workspace, eligible.ID, domain.DocumentMetadataUpdate{ExpectedUpdatedAt: eligible.UpdatedAt, Metadata: eligible.Metadata, Lifecycle: &domain.DocumentLifecycle{}})
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("runtime-filter-recovery-evidence run=%s corruption=%q status=%s requests=%d", run.ID, corruption, updated.Status, len(requests))
	}
	before, _ := s.ListRunsByWorkspace(workspace)
	for _, invalid := range []any{
		nil,
		map[string]any{"schema_version": "unknown", "where": map[string]any{"field": "document.metadata.product", "op": "eq", "value": "AgentFlow"}},
		map[string]any{"schema_version": "knowledge-metadata-v1", "where": map[string]any{"all": []any{}}},
		map[string]any{"schema_version": "knowledge-metadata-v1", "where": map[string]any{"field": "document.metadata.priority", "op": "eq", "value": json.Number("1e-16383")}},
	} {
		call(t, "/api/chat", map[string]any{"message": "rag-filter-gate", "knowledge_filter": invalid}, 400)
	}
	for _, raw := range []string{`[]`, `{"message":1}`, `{"message":"rag-filter-gate","knowledge_filter":null,"knowledge_filter":null}`} {
		call(t, "/api/chat", json.RawMessage(raw), 400)
	}
	after, _ := s.ListRunsByWorkspace(workspace)
	if len(before) != len(after) {
		t.Fatal("invalid filter created a Run")
	}
	// Archived Workspaces remain readable; a revoked membership does not.
	conv, err := s.CreateConversationInWorkspace(workspace, "Revoked before citation commit")
	if err != nil {
		t.Fatal(err)
	}
	revokeFinal.Store(true)
	denied := send(t, "/api/chat", map[string]any{"conversation_id": conv.ID, "agent_id": owned.ID, "mode": "single", "message": "rag-filter-gate ROLLBACK-742", "knowledge_filter": filter})
	select {
	case err := <-revokeResult:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("provider did not reach the pre-completion revocation boundary")
	}
	messages, err := s.ListMessages(conv.ID)
	if err != nil || len(messages) != 1 || !strings.Contains(denied.Body.String(), "event: error") {
		t.Fatalf("revoked citation completion persisted answer: messages=%d error=%v %s", len(messages), err, denied.Body.String())
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO auth_memberships(user_id,workspace_id) VALUES($1,$2)`, identity.SuperUserID, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE workspaces SET status='archived' WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	call(t, "/api/rag/search", map[string]any{"query": "ROLLBACK-742", "metadata_filter": filter}, 200)
	if _, err := db.ExecContext(t.Context(), `UPDATE workspaces SET status='active' WHERE id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM auth_memberships WHERE workspace_id=$1`, workspace); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckKnowledgeAccess(t.Context(), workspace); err == nil {
		t.Fatal("background Knowledge access retained a revoked membership")
	}
	call(t, "/api/rag/search", map[string]any{"query": "ROLLBACK-742", "metadata_filter": filter}, 404)
	if _, err := s.UpdateRunStatus(resumable.ID, domain.RunFailedRecoverable, "fixture revoked recovery"); err != nil {
		t.Fatal(err)
	}
	call(t, "/api/runs/"+resumable.ID+"/resume", map[string]any{"user_input": ""}, 404)
}
