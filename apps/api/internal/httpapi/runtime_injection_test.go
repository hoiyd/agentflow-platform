package httpapi

import (
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool/artifact"
)

type fixtureTaskStateFailure struct {
	*fixturestore.Store
	err   error
	calls int
}

func (s *fixtureTaskStateFailure) ApplyTaskStatePatch(string, domain.TaskStatePatch, domain.TaskStateSource) (domain.TaskStateRevision, error) {
	s.calls++
	return domain.TaskStateRevision{}, s.err
}

type fixtureArtifactFailure struct {
	*fixturestore.Store
	err      error
	reads    int
	searches int
}

func (s *fixtureArtifactFailure) ReadToolArtifact(string, string, int, int) (domain.ToolArtifactRead, error) {
	s.reads++
	return domain.ToolArtifactRead{}, s.err
}

func (s *fixtureArtifactFailure) SearchToolArtifact(string, string, string, int) (domain.ToolArtifactSearchResult, error) {
	s.searches++
	return domain.ToolArtifactSearchResult{}, s.err
}

// Failure inventory: the HTTP fixture must retain explicit failing services;
// omitted services must still install working defaults without inventing Stages.
func TestHTTPRuntimeFixtureOptionalServiceInjection(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "explicit failure"}[explicit], func(t *testing.T) {
			storage := fixturestore.New()
			conversation, err := storage.CreateConversation("Synthetic HTTP fixture injection")
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New("explicit service failure")
			tasks := &fixtureTaskStateFailure{Store: storage, err: failure}
			artifacts := &fixtureArtifactFailure{Store: storage, err: failure}
			options := agent.RuntimeOptions{Store: storage}
			if explicit {
				options.TaskStates = taskstate.NewService(tasks, nil)
				options.ToolArtifacts = artifact.NewService(artifacts, nil)
			}
			runtime := newRuntimeForTest(options, nil)
			prepared, err := runtime.PrepareChatRunWithContract(t.Context(), "agent_planner", conversation.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := event.WithScope(t.Context(), event.Scope{RunID: prepared.Run.ID, ConversationID: conversation.ID, TurnID: "turn-http-injection"})
			for name, arguments := range map[string]string{
				taskstate.UpdateToolName: `{"expected_version":0,"operations":[{"type":"set_goal","goal":"HTTP fixture default"}]}`,
				artifact.ReadToolName:    `{"artifact_id":"missing"}`,
				artifact.SearchToolName:  `{"artifact_id":"missing","query":"needle"}`,
			} {
				binding, ok := prepared.Catalog.ResolveReady(name)
				if !ok {
					t.Fatalf("%s Binding missing", name)
				}
				_, err := binding.Handler(ctx, json.RawMessage(arguments))
				if explicit && !errors.Is(err, failure) {
					t.Errorf("%s ignored injected failure: %v", name, err)
				}
				if !explicit && name == taskstate.UpdateToolName && err != nil {
					t.Errorf("default Task State failed: %v", err)
				}
				if !explicit && name != taskstate.UpdateToolName && err == nil {
					t.Errorf("default %s accepted missing artifact", name)
				}
			}
			if explicit && (tasks.calls != 1 || artifacts.reads != 1 || artifacts.searches != 1) {
				t.Errorf("injected calls: Task State=%d Artifact read=%d search=%d", tasks.calls, artifacts.reads, artifacts.searches)
			}
			revisions, err := storage.ListTaskStateRevisions(conversation.ID)
			want := 1
			if explicit {
				want = 0
			}
			if err != nil || len(revisions) != want {
				t.Fatalf("persisted revisions=%+v err=%v", revisions, err)
			}
			if len(revisions) == 1 && (revisions[0].Source.StageID != "" || revisions[0].Source.TurnID != "turn-http-injection") {
				t.Fatalf("Single identity changed: %+v", revisions[0])
			}
			steps, err := storage.ListCollaborationSteps(prepared.Run.ID)
			if err != nil || len(steps) != 0 {
				t.Fatalf("Single invented Stages: %+v err=%v", steps, err)
			}
		})
	}
}
