package httpapi

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/oidcfixture"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: docs/operations/resource-authorization.md. These tests
// exercise real persisted scopes; signed browser login is covered by the E2E gate.
func authorizationFixture(t *testing.T) (*Handler, *store.PostgresStore, *sql.DB, []string, []string) {
	t.Helper()
	databaseURL := pgfixture.DatabaseURL(t)
	storage, err := store.NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	if err := storage.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	idp := oidcfixture.New(t, "http://localhost:8080/api/auth/callback")
	spaces := pgfixture.GrantMemberships(t, databaseURL, idp.Server.URL, "owner", "Owner A", "Owner B")
	spaces = append(spaces, pgfixture.GrantMemberships(t, databaseURL, idp.Server.URL, "stranger", "Stranger")...)
	manager, err := identity.New(t.Context(), identity.Config{Mode: "oidc", Issuer: idp.Server.URL, ClientID: "fixture-client", RedirectURL: "http://localhost:8080/api/auth/callback", WebURL: "http://localhost:3000"}, storage)
	if err != nil {
		t.Fatal(err)
	}
	tokens := []string{strings.Repeat("a", 43), strings.Repeat("b", 43)}
	for index, subject := range []string{"owner", "stranger"} {
		hash := sha256.Sum256([]byte(tokens[index]))
		if err := storage.CreateSession(t.Context(), hex.EncodeToString(hash[:]), identity.UserID(idp.Server.URL, subject), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	provider := memorypkg.NewBuiltinProvider(storage, newLocalFallbackOpenAIClientForTest(), memorypkg.ProviderOptions{})
	if err := provider.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeMemoryProvider(t, provider) })
	return &Handler{store: storage, workspaces: storage, identity: manager, memories: provider, tools: toolManagerForEffectTest(t), runEvents: event.NewHub(16), runController: concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 2, QueueSize: 4, WaitTimeout: time.Second})}, storage, db, spaces, tokens
}

func authorizedRequest(handler http.Handler, method, path, workspace, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set(WorkspaceHeader, workspace)
	r.Header.Set("Origin", "http://localhost:3000")
	r.AddCookie(&http.Cookie{Name: "agentflow_session", Value: token})
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestMemoryProvenanceAuthorization(t *testing.T) {
	handler, storage, db, spaces, tokens := authorizationFixture(t)
	conversation, err := storage.CreateConversationInWorkspace(spaces[0], "Owned source")
	if err != nil {
		t.Fatal(err)
	}
	other, err := storage.CreateConversationInWorkspace(spaces[1], "Another space")
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	otherRun, err := storage.CreateRunWithContract("agent_planner", other.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	message, err := storage.AddMessage(conversation.ID, "user", "Source fact")
	if err != nil {
		t.Fatal(err)
	}
	otherMessage, err := storage.AddMessage(other.ID, "user", "Foreign source")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := storage.CreateConversationInWorkspace(spaces[0], "Same space different conversation")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name   string
		memory domain.Memory
		status int
	}{
		{"foreign-conversation", domain.Memory{ConversationID: other.ID}, 404},
		{"foreign-run", domain.Memory{RunID: otherRun.ID}, 404},
		{"foreign-message", domain.Memory{ConversationID: conversation.ID, SourceMessageID: otherMessage.ID}, 404},
		{"missing-conversation", domain.Memory{ConversationID: "missing"}, 404},
		{"missing-run", domain.Memory{RunID: "missing"}, 404},
		{"missing-message", domain.Memory{ConversationID: conversation.ID, SourceMessageID: "missing"}, 404},
		{"message-without-conversation", domain.Memory{SourceMessageID: message.ID}, 400},
		{"inconsistent-owned-links", domain.Memory{ConversationID: sibling.ID, RunID: run.ID}, 400},
		{"body-scope-override", domain.Memory{WorkspaceID: spaces[1]}, 400},
		{"standalone", domain.Memory{}, 201},
		{"owned-conversation", domain.Memory{ConversationID: conversation.ID}, 201},
		{"owned-linked", domain.Memory{ConversationID: conversation.ID, RunID: run.ID, SourceMessageID: message.ID}, 201},
		{"derive-conversation-from-run", domain.Memory{RunID: run.ID, SourceMessageID: message.ID}, 201},
	} {
		t.Run(item.name, func(t *testing.T) {
			item.memory.Kind, item.memory.Content = "note", "Authorization evidence "+item.name
			body, err := json.Marshal(item.memory)
			if err != nil {
				t.Fatal(err)
			}
			response := authorizedRequest(handler.Routes(), "POST", "/api/memories", spaces[0], tokens[0], string(body))
			if response.Code != item.status {
				t.Fatalf("got %d want %d: %s", response.Code, item.status, response.Body.String())
			}
		})
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM memories`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("rejected provenance persisted: count=%d err=%v", count, err)
	}
}

func TestResourceStreamRevocation(t *testing.T) {
	for _, cause := range []string{"logout", "expiry", "membership", "deleted", "storage", "workspace-storage", "changed-identity"} {
		t.Run(cause, func(t *testing.T) {
			handler, storage, db, spaces, tokens := authorizationFixture(t)
			response := authorizedRequest(handler.withIdentity(handler.withWorkspace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if err := writeSSEFrame(w, 1, "model.delta", map[string]string{"delta": "before-revocation"}); err != nil {
					t.Fatal(err)
				}
				w.(http.Flusher).Flush()
				hash := sha256.Sum256([]byte(tokens[0]))
				var err error
				switch cause {
				case "logout":
					_, err = db.Exec(`DELETE FROM auth_sessions WHERE token_hash=$1`, hex.EncodeToString(hash[:]))
				case "expiry":
					_, err = db.Exec(`UPDATE auth_sessions SET expires_at=now()-interval '1 second' WHERE token_hash=$1`, hex.EncodeToString(hash[:]))
				case "membership":
					_, err = db.Exec(`DELETE FROM auth_memberships WHERE workspace_id=$1`, spaces[0])
				case "deleted":
					_, err = db.Exec(`UPDATE workspaces SET deleted_at=now() WHERE id=$1`, spaces[0])
				case "storage":
					err = storage.Close()
				case "workspace-storage":
					_, err = db.Exec(`ALTER TABLE workspaces RENAME TO unavailable_workspaces`)
				case "changed-identity":
					_, err = db.Exec(`UPDATE auth_sessions SET user_id=(SELECT owner_user_id FROM workspaces WHERE id=$2) WHERE token_hash=$1`, hex.EncodeToString(hash[:]), spaces[2])
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := writeSSEFrame(w, 2, "model.delta", map[string]string{"delta": "must-not-leak"}); err == nil {
					t.Error("stream accepted content after access was revoked")
				}
				_, _ = w.Write([]byte(": keep-alive\n\n"))
				w.(http.Flusher).Flush()
			}))), "GET", "/api/runs/fixture/events", spaces[0], tokens[0], "")
			body := response.Body.String()
			if !strings.Contains(body, "before-revocation") || strings.Contains(body, "must-not-leak") || strings.Count(body, "event: error") != 1 {
				t.Fatalf("unsafe revoked stream: %s", body)
			}
			code := "not_found"
			if cause == "logout" || cause == "expiry" || cause == "changed-identity" {
				code = "unauthenticated"
			}
			if cause == "storage" || cause == "workspace-storage" {
				code = "service_unavailable"
			}
			if !strings.Contains(body, `"code":"`+code+`"`) {
				t.Fatalf("missing stable authorization error: %s", body)
			}
		})
	}
}

func TestMemoryProvenanceStorageFailure(t *testing.T) {
	// A lookup outage is not permission to commit. Real SQL faults isolate each
	// lookup stage without replacing the business Store or Memory provider.
	for _, table := range []string{"runs", "conversations", "messages"} {
		t.Run(table, func(t *testing.T) {
			handler, storage, db, spaces, tokens := authorizationFixture(t)
			conversation, err := storage.CreateConversationInWorkspace(spaces[0], "Fault evidence")
			if err != nil {
				t.Fatal(err)
			}
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
			if err != nil {
				t.Fatal(err)
			}
			memory := domain.Memory{Kind: "note", Content: "Must not commit", ConversationID: conversation.ID}
			if table == "runs" {
				memory.RunID = run.ID
			}
			if table == "messages" {
				memory.SourceMessageID = "source"
			}
			// The table names above are fixed test constants, never user input.
			if _, err := db.Exec(`ALTER TABLE ` + table + ` RENAME TO unavailable_` + table); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(memory)
			if err != nil {
				t.Fatal(err)
			}
			response := authorizedRequest(handler.Routes(), "POST", "/api/memories", spaces[0], tokens[0], string(body))
			if response.Code != 500 || strings.Contains(response.Body.String(), "unavailable_") {
				t.Fatalf("unsafe lookup failure: %d %s", response.Code, response.Body.String())
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM memories`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("lookup failure persisted Memory: %d %v", count, err)
			}
		})
	}
}

func TestMembershipOnlyAdapterStillProtectsStreamsAndSharedConfiguration(t *testing.T) {
	handler, _, db, spaces, tokens := authorizationFixture(t)
	handler.workspaces = nil
	for _, path := range []string{"/api/agents", "/api/agents/agent_planner", "/api/tools/get_current_time/disable"} {
		if response := authorizedRequest(handler.Routes(), "POST", path, spaces[0], tokens[0], `{}`); response.Code != 403 {
			t.Fatalf("configuration adapter bypass: %d %s", response.Code, response.Body.String())
		}
	}
	for _, cause := range []string{"membership", "storage"} {
		response := authorizedRequest(handler.withIdentity(handler.withWorkspace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if err := writeSSEFrame(w, 0, "model.delta", map[string]string{"delta": "allowed"}); err != nil {
				t.Fatal(err)
			}
			statement := `DELETE FROM auth_memberships WHERE workspace_id=$1`
			if cause == "storage" {
				statement = `ALTER TABLE auth_memberships RENAME TO unavailable_memberships`
			}
			var err error
			if cause == "storage" {
				_, err = db.Exec(statement)
			} else {
				_, err = db.Exec(statement, spaces[0])
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := writeSSEFrame(w, 0, "model.delta", map[string]string{"delta": "must-not-leak"}); err == nil {
				t.Error("membership-only stream leaked content")
			}
		}))), "GET", "/api/runs/fixture/events", spaces[0], tokens[0], "")
		if strings.Contains(response.Body.String(), "must-not-leak") || !strings.Contains(response.Body.String(), "event: error") {
			t.Fatalf("unsafe membership adapter: %s", response.Body.String())
		}
		if cause == "membership" {
			if _, err := db.Exec(`INSERT INTO auth_memberships(user_id,workspace_id) SELECT owner_user_id,id FROM workspaces WHERE id=$1`, spaces[0]); err != nil {
				t.Fatal(err)
			}
		}
	}
}
