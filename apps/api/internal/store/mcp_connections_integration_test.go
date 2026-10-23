package store

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestMCPConnectionMigrationPreservesExistingData(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	workspace := testOwnedWorkspace(t, s)
	conversation, err := s.CreateConversationInWorkspace(workspace, "Existing conversation")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an existing deployment before this new table was introduced.
	if _, err := s.db.Exec(`DROP TABLE mcp_connections`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := s.SaveMCPConnection(t.Context(), domain.MCPConnection{WorkspaceID: workspace, Name: "Docs", URL: "https://docs.example/mcp", AuthMode: "none", Status: "configured", ReadOnlyTools: []string{}, Discovery: []domain.MCPToolDefinition{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	saved, err := s.GetMCPConnection(t.Context(), workspace, connection.ID)
	if err != nil || saved.ID != connection.ID || saved.Revision != 1 || saved.Name != "Docs" {
		t.Fatalf("round trip: %#v %v", saved, err)
	}
	if current, ok, err := s.GetConversation(conversation.ID); err != nil || !ok || current.WorkspaceID != workspace {
		t.Fatalf("migration damaged existing conversation: %#v %v", current, err)
	}
	if err := s.DeleteMCPConnection(t.Context(), workspace, connection.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetMCPConnection(t.Context(), workspace, connection.ID); !IsNotFound(err) {
		t.Fatalf("deleted connection remained readable: %v", err)
	}
}
