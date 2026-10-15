package app

import (
	"errors"
	"testing"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

var planPersistenceUnavailable = errors.New("plan persistence unavailable")

// Fault inventory: failure of either Run read or evidence read, missing linked
// resources/validator and changed Agent must reject before a message/Run write.
// Base data comes from the production HTTP/Binding execution in the caller.
func assertPlanningPersistenceFailures(t *testing.T, s *store.PostgresStore, run domain.Run, plan domain.RunPlan) {
	t.Helper()
	scope := domain.NewWorkspaceScope(run.WorkspaceID)
	other, err := s.ForWorkspace(scope).CreateAgent(domain.Agent{Name: "Other plan owner"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		other.Archived = true
		if _, err := s.UpdateAgent(other); err != nil {
			t.Error(err)
		}
	}()
	for _, scenario := range []string{"plan read", "execution read", "event read", "missing plan", "different Agent", "missing validator"} {
		t.Run(scenario, func(t *testing.T) {
			fault := &planningFailureStore{WorkspaceStore: s.ForWorkspace(scope)}
			req := domain.ChatRequest{AgentID: run.RuntimeSnapshot.Agent.ID, ConversationID: run.ConversationID, Message: "Execute plan", PlanOrigin: &domain.PlanOrigin{RunID: run.ID, Hash: plan.Hash}}
			switch scenario {
			case "plan read":
				fault.failGet = 1
			case "execution read":
				fault.failGet = 2
			case "event read":
				fault.failEvents = true
			case "missing plan":
				req.PlanOrigin.RunID = "missing"
			case "different Agent":
				req.AgentID = other.ID
			}
			before, err := s.ListMessages(run.ConversationID)
			if err != nil {
				t.Fatal(err)
			}
			commands := runcommands.Commands{Store: planningFailureProvider{fault}, Capacity: concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})}
			err = commands.Start(t.Context(), runcommands.Scope{Workspace: scope, Owner: "super"}, req, nil, nil, runcommands.Observer{})
			if err == nil {
				t.Fatal("failed planning boundary started execution")
			}
			if fault.failGet != 0 || fault.failEvents {
				if !errors.Is(err, planPersistenceUnavailable) {
					t.Fatal(err)
				}
			}
			after, err := s.ListMessages(run.ConversationID)
			if err != nil || len(before) != len(after) {
				t.Fatalf("rejected approval wrote messages: %v", err)
			}
		})
	}
	// Recording failure must not mark a planning Run completed or leave a
	// seemingly consumable plan. Only persistence is faulted; lifecycle is real.
	for _, scenario := range []string{"event read", "event write", "empty proposal"} {
		t.Run(scenario, func(t *testing.T) {
			created, err := s.CreateRunWithContract(run.RuntimeSnapshot.Agent.ID, run.ConversationID, *run.RuntimeSnapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			fault := &planningFailureStore{WorkspaceStore: s.ForWorkspace(scope), failEvents: scenario == "event read", failWrite: scenario == "event write"}
			output := "A bounded proposal"
			if scenario == "empty proposal" {
				output = ""
			}
			if _, err := runcompletion.Resolve(t.Context(), fault, runcompletion.Dependencies{}, created.ID, "Original task", output); err == nil {
				t.Fatal("recording failure accepted completion")
			}
			stored, found, err := s.GetRun(created.ID)
			if err != nil || !found || stored.Status != domain.RunQueued {
				t.Fatalf("record failure changed status: %+v %v", stored, err)
			}
			items, err := s.ListRunEvents(created.ID)
			if err != nil || len(items) != 0 {
				t.Fatalf("partial plan persisted: %+v %v", items, err)
			}
		})
	}
}

type planningFailureProvider struct{ scoped store.WorkspaceStore }

func (p planningFailureProvider) ForWorkspace(domain.WorkspaceScope) store.WorkspaceStore {
	return p.scoped
}

type planningFailureStore struct {
	store.WorkspaceStore
	getCalls, failGet     int
	failEvents, failWrite bool
}

func (s *planningFailureStore) GetRun(id string) (domain.Run, bool, error) {
	s.getCalls++
	if s.getCalls == s.failGet {
		return domain.Run{}, false, planPersistenceUnavailable
	}
	return s.WorkspaceStore.GetRun(id)
}
func (s *planningFailureStore) ListRunEvents(id string) ([]domain.RunEvent, error) {
	if s.failEvents {
		return nil, planPersistenceUnavailable
	}
	return s.WorkspaceStore.ListRunEvents(id)
}
func (s *planningFailureStore) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	if s.failWrite {
		return domain.RunEvent{}, planPersistenceUnavailable
	}
	return s.WorkspaceStore.CreateRunEvent(item)
}
