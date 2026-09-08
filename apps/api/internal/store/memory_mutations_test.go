package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func mutationFixture(t *testing.T, backend string) (Store, func() Store, domain.Memory, domain.MemoryEmbedding) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "memory.json")
	open := func() Store {
		t.Helper()
		if backend == "postgres" {
			return openPostgresTestStore(t)
		}
		s, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	conv, err := s.CreateConversation("memory mutations")
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.AddMessage(conv.ID, "user", "Remember that the release day is Monday.")
	if err != nil {
		t.Fatal(err)
	}
	vector := make([]float64, 1536)
	vector[0] = 1
	embedding := domain.MemoryEmbedding{Provider: "test", Model: "unit", Dimensions: len(vector), Embedding: vector}
	m, err := s.CreateMemory(domain.Memory{Kind: "fact", Content: "release day is Monday", SourceMessageID: message.ID, ConversationID: conv.ID, Metadata: map[string]any{"source": "test"}}, embedding)
	if err != nil {
		t.Fatal(err)
	}
	return s, open, m, embedding
}

func mutationCommand(m domain.Memory) domain.MemoryMutation {
	return domain.MemoryMutation{OperationID: "correct-" + m.ID, ExpectedVersion: m.Version, Action: "replace", Content: "release day is Tuesday", Actor: "operator", Reason: "corrected schedule"}
}

func TestMemoryMutationStoreContract(t *testing.T) {
	for _, backend := range []string{"file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, reopen, m, embedding := mutationFixture(t, backend)
			if duplicate, err := s.CreateMemory(m, embedding); err != nil || duplicate.Version != 1 {
				t.Fatalf("duplicate create: %+v %v", duplicate, err)
			}
			altered := m
			altered.Content = "changed create"
			if _, err := s.CreateMemory(altered, embedding); !errors.Is(err, ErrMemoryConflict) {
				t.Fatalf("create overwrite: %v", err)
			}
			if _, err := s.CreateMemory(domain.Memory{Kind: "fact", Content: "unrelated memory"}, embedding); err != nil {
				t.Fatal(err)
			}
			candidate := domain.MemoryCandidate{ID: "candidate-" + m.ID, SourceMessageID: m.SourceMessageID, SourceRole: "user", Kind: m.Kind, Content: m.Content, Status: domain.MemoryCandidateAccepted}
			if _, _, err := s.CreateMemoryCandidate(candidate); err != nil {
				t.Fatal(err)
			}
			cmd := mutationCommand(m)
			cmd.Actor, cmd.Reason = "token=actor-secret", "api_key=reason-secret"
			r, err := s.MutateMemory(m.WorkspaceID, m.ID, cmd, embedding)
			if err != nil || !r.Applied || r.Memory.Version != 2 || r.Change.PreviousVersion != 1 {
				t.Fatalf("replace: %+v %v", r, err)
			}
			if strings.Contains(r.Change.Actor, "actor-secret") || strings.Contains(r.Change.Reason, "reason-secret") {
				t.Fatal("audit leaked secret")
			}
			s = reopen()
			detail, err := s.GetMemoryDetail(m.WorkspaceID, m.ID)
			if err != nil || len(detail.Changes) != 1 || detail.Memory.Content != cmd.Content || detail.Changes[0].CommandHash == "" {
				t.Fatalf("round trip: %+v %v", detail, err)
			}
			if change, err := s.FindMemoryChange(m.WorkspaceID, cmd.OperationID); err != nil || change == nil || change.Version != 2 {
				t.Fatalf("find change: %+v %v", change, err)
			}
			items, err := s.SearchMemories(domain.MemorySearch{Embedding: embedding.Embedding, Metadata: map[string]string{"source": "test"}, Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, item := range items {
				if item.Memory.ID == m.ID {
					found = true
					if item.Memory.Content != cmd.Content || item.Memory.Version != 2 {
						t.Fatal("stale recall")
					}
				}
			}
			if !found {
				t.Fatal("reopened store lost embedding")
			}
			r, err = s.MutateMemory(m.WorkspaceID, m.ID, cmd, domain.MemoryEmbedding{})
			if err != nil || r.Applied || r.Memory.Version != 2 {
				t.Fatalf("duplicate: %+v %v", r, err)
			}
			changed := cmd
			changed.Content = "different"
			if _, err := s.MutateMemory(m.WorkspaceID, m.ID, changed, embedding); !errors.Is(err, ErrMemoryConflict) {
				t.Fatalf("command ID reuse: %v", err)
			}
			changed = cmd
			changed.OperationID += "-stale"
			if _, err := s.MutateMemory(m.WorkspaceID, m.ID, changed, embedding); !errors.Is(err, ErrMemoryConflict) {
				t.Fatalf("stale version: %v", err)
			}
			if _, err := s.GetMemoryDetail("another-workspace", m.ID); !errors.Is(err, ErrMemoryMissing) {
				t.Fatalf("detail scope: %v", err)
			}
			if _, err := s.MutateMemory("another-workspace", m.ID, cmd, embedding); !errors.Is(err, ErrMemoryMissing) {
				t.Fatalf("mutation scope: %v", err)
			}
			if change, err := s.FindMemoryChange("another-workspace", cmd.OperationID); err != nil || change != nil {
				t.Fatal("audit leaked workspace")
			}
			deleted := domain.MemoryMutation{OperationID: "delete-" + m.ID, ExpectedVersion: 2, Action: "delete", Actor: "operator", Reason: "outdated"}
			r, err = s.MutateMemory(m.WorkspaceID, m.ID, deleted, domain.MemoryEmbedding{})
			if err != nil || r.Memory.DeletedAt == nil || r.Memory.Content != "" || len(r.Memory.Metadata) != 0 || r.Memory.Version != 3 {
				t.Fatalf("delete: %+v %v", r, err)
			}
			s = reopen()
			detail, err = s.GetMemoryDetail(m.WorkspaceID, m.ID)
			if err != nil || detail.Memory.DeletedAt == nil || len(detail.Changes) != 2 || detail.Changes[0].Action != "delete" {
				t.Fatalf("deleted roundtrip: %+v %v", detail, err)
			}
			encoded, _ := json.Marshal(detail)
			if strings.Contains(string(encoded), m.Content) || strings.Contains(string(encoded), cmd.Content) {
				t.Fatal("audit retains original content")
			}
			if r, err = s.MutateMemory(m.WorkspaceID, m.ID, deleted, domain.MemoryEmbedding{}); err != nil || r.Applied {
				t.Fatalf("duplicate deletion: %v", err)
			}
			if r, err = s.MutateMemory(m.WorkspaceID, m.ID, cmd, domain.MemoryEmbedding{}); err != nil || r.Applied || r.Memory.DeletedAt == nil {
				t.Fatalf("old duplicate resurrected: %v", err)
			}
			if _, err = s.CreateMemory(m, embedding); !errors.Is(err, ErrMemoryConflict) {
				t.Fatalf("same ID resurrection: %v", err)
			}
			m.ID += "-late"
			if _, err = s.CreateMemory(m, embedding); !errors.Is(err, ErrMemoryConflict) {
				t.Fatalf("late source resurrection: %v", err)
			}
			candidate.ID += "-late"
			late, created, err := s.CreateMemoryCandidate(candidate)
			if err != nil || !created || late.Status != domain.MemoryCandidateRejected || late.Content == candidate.Content {
				t.Fatalf("late candidate: %+v %v", late, err)
			}
			candidates, err := s.ListMemoryCandidates("")
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range candidates {
				if c.SourceMessageID == m.SourceMessageID && (c.Content == m.Content || c.Status != domain.MemoryCandidateRejected) {
					t.Fatal("old candidate not withdrawn")
				}
			}
			items, err = s.SearchMemories(domain.MemorySearch{Embedding: embedding.Embedding, Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if item.Memory.SourceMessageID == m.SourceMessageID {
					t.Fatal("deleted memory recalled")
				}
			}
			if err := s.DeleteConversation(m.ConversationID); err != nil {
				t.Fatal(err)
			}
			candidate.ConversationID = ""
			candidate.ID += "-after-source-delete"
			late, _, err = s.CreateMemoryCandidate(candidate)
			if err != nil || late.Status != domain.MemoryCandidateRejected {
				t.Fatalf("source deletion lost fence: %+v %v", late, err)
			}
		})
	}
}

func TestMemoryConcurrentCorrectionsAndDuplicates(t *testing.T) {
	for _, backend := range []string{"file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, _, m, embedding := mutationFixture(t, backend)
			out := make(chan error, 8)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Go(func() {
					cmd := mutationCommand(m)
					cmd.OperationID += fmt.Sprint(i)
					_, err := s.MutateMemory(m.WorkspaceID, m.ID, cmd, embedding)
					out <- err
				})
			}
			wg.Wait()
			close(out)
			succeeded := 0
			for err := range out {
				if err == nil {
					succeeded++
				} else if !errors.Is(err, ErrMemoryConflict) {
					t.Fatal(err)
				}
			}
			if succeeded != 1 {
				t.Fatalf("winners=%d", succeeded)
			}
			detail, err := s.GetMemoryDetail(m.WorkspaceID, m.ID)
			if err != nil || len(detail.Changes) != 1 || detail.Memory.Version != 2 {
				t.Fatalf("concurrent result: %+v %v", detail, err)
			}
			command := mutationCommand(detail.Memory)
			command.OperationID = "duplicate-" + m.ID
			applied := make(chan bool, 8)
			for i := 0; i < 8; i++ {
				wg.Go(func() {
					result, err := s.MutateMemory(m.WorkspaceID, m.ID, command, embedding)
					if err != nil {
						t.Error(err)
					}
					applied <- result.Applied
				})
			}
			wg.Wait()
			close(applied)
			writes := 0
			for value := range applied {
				if value {
					writes++
				}
			}
			if writes != 1 {
				t.Fatalf("duplicate command wrote %d times", writes)
			}
			detail, err = s.GetMemoryDetail(m.WorkspaceID, m.ID)
			if err != nil || detail.Memory.Version != 3 || len(detail.Changes) != 2 {
				t.Fatalf("duplicate receipts: %+v %v", detail, err)
			}
		})
	}
}

func TestFileMemoryMutationRollback(t *testing.T) {
	s, _, m, embedding := mutationFixture(t, "file")
	f := s.(*FileStore)
	before, _ := json.Marshal(f.data)
	original := f.path
	f.path = filepath.Join(t.TempDir(), "missing", "memory.json")
	if _, err := f.MutateMemory(m.WorkspaceID, m.ID, mutationCommand(m), embedding); err == nil {
		t.Fatal("expected save failure")
	}
	after, _ := json.Marshal(f.data)
	if string(before) != string(after) {
		t.Fatal("failed mutation changed in-memory state")
	}
	if _, err := f.CreateMemory(domain.Memory{Kind: "fact", Content: "failed create"}, embedding); err == nil {
		t.Fatal("create save error missing")
	}
	if _, _, err := f.CreateMemoryCandidate(domain.MemoryCandidate{ID: "failed-proposal", SourceMessageID: "new", SourceRole: "user", Kind: "fact", Content: "failed proposal", Status: domain.MemoryCandidateAccepted}); err == nil {
		t.Fatal("candidate save error missing")
	}
	f.path = original
	if _, err := f.MutateMemory(m.WorkspaceID, m.ID, mutationCommand(m), embedding); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryMutationValidation(t *testing.T) {
	valid := mutationCommand(domain.Memory{ID: "id", Version: 1})
	for _, mutate := range []func(*domain.MemoryMutation){
		func(c *domain.MemoryMutation) { c.OperationID = "" }, func(c *domain.MemoryMutation) { c.OperationID = "Bearer secret" },
		func(c *domain.MemoryMutation) { c.OperationID = strings.Repeat("x", 129) }, func(c *domain.MemoryMutation) { c.ExpectedVersion = 0 },
		func(c *domain.MemoryMutation) { c.Actor = " " }, func(c *domain.MemoryMutation) { c.Actor = strings.Repeat("x", 129) },
		func(c *domain.MemoryMutation) { c.Reason = "" }, func(c *domain.MemoryMutation) { c.Reason = strings.Repeat("x", 513) },
		func(c *domain.MemoryMutation) { c.Action = "add" }, func(c *domain.MemoryMutation) { c.Action = "delete" },
		func(c *domain.MemoryMutation) { c.Content = " " }, func(c *domain.MemoryMutation) { c.Content = strings.Repeat("x", 8001) },
	} {
		cmd := valid
		mutate(&cmd)
		if _, err := NormalizeMemoryMutation(cmd); !errors.Is(err, ErrMemoryMutationInvalid) {
			t.Fatalf("accepted %+v", cmd)
		}
	}
	s, _, m, embedding := mutationFixture(t, "file")
	if _, err := s.MutateMemory(m.WorkspaceID, m.ID, domain.MemoryMutation{}, embedding); !errors.Is(err, ErrMemoryMutationInvalid) {
		t.Fatal(err)
	}
	if _, err := s.MutateMemory(m.WorkspaceID, m.ID, mutationCommand(m), domain.MemoryEmbedding{}); !errors.Is(err, ErrMemoryMutationInvalid) {
		t.Fatal(err)
	}
}

func TestMemoryMutationSchemaMigration(t *testing.T) {
	all := strings.Join(postgresMigrations, "\n")
	for _, fragment := range []string{"ALTER TABLE memories ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1", "ALTER TABLE memories ADD COLUMN IF NOT EXISTS deleted_at timestamptz", "CREATE TABLE IF NOT EXISTS memory_changes", "PRIMARY KEY (workspace_id, operation_id)", "ALTER TABLE memory_candidates ADD COLUMN IF NOT EXISTS workspace_id text"} {
		if !strings.Contains(all, fragment) {
			t.Fatalf("missing %s", fragment)
		}
	}
}

func TestMemorySourceFenceIsWorkspaceScoped(t *testing.T) {
	for _, backend := range []string{"file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			s, _, m, embedding := mutationFixture(t, backend)
			otherWorkspace := "other-" + m.ID
			candidate := domain.MemoryCandidate{ID: "foreign-" + m.ID, WorkspaceID: otherWorkspace, SourceMessageID: m.SourceMessageID, SourceRole: "user", Kind: "fact", Content: "foreign fact", Status: domain.MemoryCandidateAccepted}
			if _, _, err := s.CreateMemoryCandidate(candidate); err != nil {
				t.Fatal(err)
			}
			command := domain.MemoryMutation{OperationID: "delete-" + m.ID, Action: "delete", ExpectedVersion: 1, Actor: "user", Reason: "wrong"}
			if _, err := s.MutateMemory(m.WorkspaceID, m.ID, command, domain.MemoryEmbedding{}); err != nil {
				t.Fatal(err)
			}
			candidates, err := s.ListMemoryCandidates("")
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range candidates {
				if c.ID == candidate.ID && (c.Status != domain.MemoryCandidateAccepted || c.Content != candidate.Content) {
					t.Fatal("cross-workspace candidate scrubbed")
				}
			}
			candidate.ID += "-late"
			late, _, err := s.CreateMemoryCandidate(candidate)
			if err != nil || late.Status != domain.MemoryCandidateAccepted {
				t.Fatalf("foreign candidate blocked: %+v %v", late, err)
			}
			candidate.WorkspaceID = m.WorkspaceID
			if _, _, err := s.CreateMemoryCandidate(candidate); !errors.Is(err, ErrMemoryConflict) {
				t.Fatalf("candidate identity crossed workspace: %v", err)
			}
			foreign, err := s.CreateMemory(domain.Memory{WorkspaceID: otherWorkspace, SourceMessageID: m.SourceMessageID, Content: "foreign fact", Kind: "fact"}, embedding)
			if err != nil {
				t.Fatalf("foreign materialization blocked: %v", err)
			}
			// The same command ID is independent in another Workspace.
			if _, err := s.MutateMemory(otherWorkspace, foreign.ID, command, domain.MemoryEmbedding{}); err != nil {
				t.Fatalf("operation ID scope: %v", err)
			}
		})
	}
}

func TestMemoryCandidateWorkspaceUpgrade(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		f, err := NewFileStore(t.TempDir() + "/memory.json")
		if err != nil {
			t.Fatal(err)
		}
		conv, err := f.CreateConversationInWorkspace("team", "legacy candidates")
		if err != nil {
			t.Fatal(err)
		}
		f.data.MemoryCandidates = []domain.MemoryCandidate{{ID: "legacy", ConversationID: conv.ID}, {ID: "orphan"}}
		if err := f.saveLocked(); err != nil {
			t.Fatal(err)
		}
		f, err = NewFileStore(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if f.data.MemoryCandidates[0].WorkspaceID != "team" || f.data.MemoryCandidates[1].WorkspaceID != domain.DefaultWorkspaceID {
			t.Fatalf("backfill: %+v", f.data.MemoryCandidates)
		}
	})
	t.Run("postgres", func(t *testing.T) {
		base := openPostgresTestStore(t)
		schema := NewID("h17_upgrade")
		if _, err := base.db.Exec(`CREATE SCHEMA ` + schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := base.db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
				t.Error(err)
			}
		})
		u, err := url.Parse(os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema+",public")
		u.RawQuery = q.Encode()
		pg, err := NewPostgresStore(u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = pg.Close() })
		conv, err := pg.CreateConversationInWorkspace("team", "legacy")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pg.db.Exec(`ALTER TABLE memory_candidates DROP COLUMN workspace_id`); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.db.Exec(`ALTER TABLE memories DROP COLUMN version, DROP COLUMN deleted_at`); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.db.Exec(`INSERT INTO memory_candidates(id,conversation_id,source_message_id,source_role,kind,content,status,extraction_reason,policy_reason,created_at) VALUES ('legacy',$1,'source','user','fact','legacy fact','accepted','','',now())`, conv.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pg.db.Exec(`INSERT INTO memories(id,workspace_id,kind,content,created_at,updated_at) VALUES ('legacy','team','fact','legacy fact',now(),now())`); err != nil {
			t.Fatal(err)
		}
		upgraded, err := NewPostgresStore(u.String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = upgraded.Close() })
		candidates, err := upgraded.ListMemoryCandidates(conv.ID)
		if err != nil || len(candidates) != 1 || candidates[0].WorkspaceID != "team" {
			t.Fatalf("backfill: %+v %v", candidates, err)
		}
		detail, err := upgraded.GetMemoryDetail("team", "legacy")
		if err != nil || detail.Memory.Version != 1 || detail.Memory.DeletedAt != nil {
			t.Fatalf("memory upgrade: %+v %v", detail, err)
		}
	})
}

func TestPostgresMemoryMutationRollback(t *testing.T) {
	s, _, m, embedding := mutationFixture(t, "postgres")
	pg := s.(*PostgresStore)
	if _, err := pg.MutateMemory(m.WorkspaceID, m.ID, domain.MemoryMutation{}, embedding); !errors.Is(err, ErrMemoryMutationInvalid) {
		t.Fatal(err)
	}
	if _, err := pg.MutateMemory(m.WorkspaceID, m.ID, mutationCommand(m), domain.MemoryEmbedding{}); !errors.Is(err, ErrMemoryMutationInvalid) {
		t.Fatal(err)
	}
	// A non-finite vector fails in pgvector after UPDATE and DELETE; both must roll back.
	embedding.Embedding[0] = 1e308
	if _, err := s.MutateMemory(m.WorkspaceID, m.ID, mutationCommand(m), embedding); err == nil {
		t.Fatal("expected pgvector range failure")
	}
	detail, err := s.GetMemoryDetail(m.WorkspaceID, m.ID)
	if err != nil || detail.Memory.Version != 1 || len(detail.Changes) != 0 {
		t.Fatalf("partial transaction: %+v %v", detail, err)
	}
	var count int
	if err := pg.db.QueryRow(`SELECT count(*) FROM memory_embeddings WHERE memory_id=$1`, m.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("lost vector: %d %v", count, err)
	}
	if err := pg.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.GetMemoryDetail(m.WorkspaceID, m.ID); err == nil {
		t.Fatal("closed detail")
	}
	if _, err := pg.FindMemoryChange(m.WorkspaceID, "op"); err == nil {
		t.Fatal("closed audit")
	}
	if _, err := pg.MutateMemory(m.WorkspaceID, m.ID, mutationCommand(m), embedding); err == nil {
		t.Fatal("closed mutation")
	}
	if _, _, err := pg.CreateMemoryCandidate(domain.MemoryCandidate{ID: "closed", SourceMessageID: "source", SourceRole: "user", Kind: "fact", Content: "value", Status: domain.MemoryCandidateAccepted}); err == nil {
		t.Fatal("closed candidate")
	}
}

func TestPostgresMemoryMutationWriteFailuresAreAtomic(t *testing.T) {
	for _, tc := range []struct{ table, event, column string }{
		{"memories", "UPDATE", "id"}, {"memory_embeddings", "DELETE", "memory_id"},
		{"memory_candidates", "UPDATE", "source_message_id"}, {"memory_changes", "INSERT", "memory_id"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			s, _, m, embedding := mutationFixture(t, "postgres")
			pg := s.(*PostgresStore)
			candidate := domain.MemoryCandidate{ID: "candidate-" + m.ID, SourceMessageID: m.SourceMessageID, SourceRole: "user", Kind: "fact", Content: "original proposal", Status: domain.MemoryCandidateAccepted}
			if _, _, err := pg.CreateMemoryCandidate(candidate); err != nil {
				t.Fatal(err)
			}
			name := NewID("h17_fault")
			if _, err := pg.db.Exec(`CREATE FUNCTION ` + name + `() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected memory write failure'; END $$`); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, err := pg.db.Exec(`DROP FUNCTION ` + name + `() CASCADE`)
				if err != nil {
					t.Error(err)
				}
			})
			row := "OLD"
			if tc.event == "INSERT" {
				row = "NEW"
			}
			id := m.ID
			if tc.table == "memory_candidates" {
				id = m.SourceMessageID
			}
			// Table/column names are fixed above; ID comes only from the test store.
			query := fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s FOR EACH ROW WHEN (%s.%s = '%s') EXECUTE FUNCTION %s()", name, tc.event, tc.table, row, tc.column, id, name)
			if _, err := pg.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.MutateMemory(m.WorkspaceID, m.ID, mutationCommand(m), embedding); err == nil {
				t.Fatal("write failure ignored")
			}
			detail, err := pg.GetMemoryDetail(m.WorkspaceID, m.ID)
			if err != nil || detail.Memory.Version != 1 || detail.Memory.Content != m.Content || len(detail.Changes) != 0 {
				t.Fatalf("partial write: %+v %v", detail, err)
			}
			var vectorCount int
			if err := pg.db.QueryRow(`SELECT count(*) FROM memory_embeddings WHERE memory_id=$1`, m.ID).Scan(&vectorCount); err != nil || vectorCount != 1 {
				t.Fatalf("lost vector: %d %v", vectorCount, err)
			}
			var content string
			if err := pg.db.QueryRow(`SELECT content FROM memory_candidates WHERE id=$1`, candidate.ID).Scan(&content); err != nil || content != candidate.Content {
				t.Fatalf("scrubbed candidate on failure: %s %v", content, err)
			}
		})
	}
}

func TestLegacyFileMemoryVersionDefaultsToOne(t *testing.T) {
	s, _, m, _ := mutationFixture(t, "file")
	f := s.(*FileStore)
	f.data.Memories[0].Version = 0
	if err := f.saveLocked(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version": 0`) {
		t.Fatal("fixture")
	}
	reloaded, err := NewFileStore(f.path)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reloaded.GetMemoryDetail(m.WorkspaceID, m.ID)
	if err != nil || detail.Memory.Version != 1 {
		t.Fatalf("legacy version: %+v %v", detail, err)
	}
}
