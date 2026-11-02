package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool/artifact"
)

type injectedTaskStateStore struct {
	*fixturestore.Store
	failure error
	calls   int
}

func (s *injectedTaskStateStore) ApplyTaskStatePatch(id string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error) {
	s.calls++
	if s.failure != nil {
		return domain.TaskStateRevision{}, s.failure
	}
	return s.Store.ApplyTaskStatePatch(id, patch, source)
}

type injectedArtifactStore struct {
	*fixturestore.Store
	failure  error
	reads    int
	searches int
}

func (s *injectedArtifactStore) ReadToolArtifact(runID, id string, offset, limit int) (domain.ToolArtifactRead, error) {
	s.reads++
	if s.failure != nil {
		return domain.ToolArtifactRead{}, s.failure
	}
	return s.Store.ReadToolArtifact(runID, id, offset, limit)
}

func (s *injectedArtifactStore) SearchToolArtifact(runID, id, query string, limit int) (domain.ToolArtifactSearchResult, error) {
	s.searches++
	if s.failure != nil {
		return domain.ToolArtifactSearchResult{}, s.failure
	}
	return s.Store.SearchToolArtifact(runID, id, query, limit)
}

// Failure inventory: fixture defaults must not replace explicit services, hide
// their errors, write on failed Apply, or invent a Stage for Single execution.
func TestRuntimeFixtureOptionalServiceInjection(t *testing.T) {
	for _, mode := range []string{"single", "staged"} {
		for _, injection := range []string{"default", "explicit", "failure"} {
			t.Run(mode+"/"+injection, func(t *testing.T) {
				storage := fixturestore.New()
				conversation, err := storage.CreateConversation("Synthetic runtime injection")
				if err != nil {
					t.Fatal(err)
				}
				tasks := &injectedTaskStateStore{Store: storage}
				artifacts := &injectedArtifactStore{Store: storage}
				var wantErr error
				if injection == "failure" {
					wantErr = errors.New("injected capability unavailable")
					tasks.failure, artifacts.failure = wantErr, wantErr
				}
				options := RuntimeOptions{Store: storage}
				if injection != "default" {
					options.TaskStates = taskstate.NewService(tasks, event.StoreSink{Store: storage})
					options.ToolArtifacts = artifact.NewService(artifacts, event.NewRecorder(storage))
				}
				runtime := newRuntimeForTest(options, nil)
				var prepared PreparedRun
				stageID := ""
				if mode == "staged" {
					collaboration, err := runtime.PrepareCollaborationRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
					if err != nil {
						t.Fatal(err)
					}
					prepared.Run = collaboration.Run
					step, err := storage.CreateCollaborationStep(domain.CollaborationStep{RunID: prepared.Run.ID, ConversationID: conversation.ID, Role: "worker", Status: domain.CollaborationStepRunning})
					if err != nil {
						t.Fatal(err)
					}
					stageID = step.ID
					prepared.Catalog, err = runtime.currentCatalog("")
				} else {
					prepared, err = runtime.PrepareChatRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
				}
				if err != nil {
					t.Fatal(err)
				}
				ctx := event.WithScope(t.Context(), event.Scope{RunID: prepared.Run.ID, ConversationID: conversation.ID, StageID: stageID, TurnID: "turn-injection"})
				binding, ok := prepared.Catalog.ResolveReady(taskstate.UpdateToolName)
				if !ok {
					t.Fatal("Task State Binding missing")
				}
				_, err = binding.Handler(ctx, json.RawMessage(`{"expected_version":0,"operations":[{"type":"set_goal","goal":"Preserve injection"}]}`))
				if !errors.Is(err, wantErr) {
					t.Errorf("Task State error=%v want=%v", err, wantErr)
				}
				revisions, err := storage.ListTaskStateRevisions(conversation.ID)
				wantRevisions := 1
				if wantErr != nil {
					wantRevisions = 0
				}
				if err != nil || len(revisions) != wantRevisions {
					t.Errorf("persisted revisions=%+v err=%v", revisions, err)
				}
				if len(revisions) == 1 && (revisions[0].State.Goal != "Preserve injection" || revisions[0].Source.StageID != stageID || revisions[0].Source.TurnID != "turn-injection") {
					t.Fatalf("persisted state/identity changed: %+v", revisions[0])
				}
				expires := time.Now().Add(time.Hour)
				content := []byte("injection needle")
				stored, err := storage.CreateToolArtifact(domain.ToolArtifact{
					ID: "injection-artifact", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
					RunID: prepared.Run.ID, ToolCallID: "source", ToolName: "fixture", MediaType: "text/plain",
					ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(content)), OriginalByteSize: len(content), StoredByteSize: len(content),
					CreatedAt: time.Now().UTC(), ExpiresAt: &expires,
				}, content)
				if err != nil {
					t.Fatal(err)
				}
				for name, arguments := range map[string]string{
					artifact.ReadToolName:   `{"artifact_id":"` + stored.ID + `"}`,
					artifact.SearchToolName: `{"artifact_id":"` + stored.ID + `","query":"needle"}`,
				} {
					binding, ok := prepared.Catalog.ResolveReady(name)
					if !ok {
						t.Fatalf("%s Binding missing", name)
					}
					result, err := binding.Handler(ctx, json.RawMessage(arguments))
					if !errors.Is(err, wantErr) || (err == nil && result == nil) {
						t.Errorf("%s result=%+v error=%v want=%v", name, result, err, wantErr)
					}
					if err == nil {
						switch result := result.(type) {
						case domain.ToolArtifactRead:
							if result.Content != string(content) {
								t.Errorf("artifact content=%q", result.Content)
							}
						case domain.ToolArtifactSearchResult:
							if len(result.Matches) != 1 {
								t.Errorf("artifact matches=%+v", result.Matches)
							}
						default:
							t.Errorf("unexpected artifact result %T", result)
						}
					}
				}
				read, err := storage.ReadToolArtifact(prepared.Run.ID, stored.ID, 0, 100)
				if err != nil || read.Content != string(content) {
					t.Fatalf("artifact changed: %+v err=%v", read, err)
				}
				if injection != "default" && (tasks.calls != 1 || artifacts.reads != 1 || artifacts.searches != 1) {
					t.Errorf("injected calls: Task State=%d Artifact read=%d search=%d", tasks.calls, artifacts.reads, artifacts.searches)
				}
				if mode == "single" {
					steps, err := storage.ListCollaborationSteps(prepared.Run.ID)
					if err != nil || len(steps) != 0 {
						t.Fatalf("Single invented Stages: %+v err=%v", steps, err)
					}
				}
			})
		}
	}
}
