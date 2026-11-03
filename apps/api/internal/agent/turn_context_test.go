package agent

import (
	"errors"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

type contextTaskStateStore struct {
	*fixturestore.Store
	goal  string
	err   error
	reads int
}

func (s *contextTaskStateStore) GetTaskState(id string) (domain.TaskState, bool, error) {
	s.reads++
	return domain.TaskState{ConversationID: id, Version: 1, Goal: s.goal}, true, s.err
}

// Failure inventory: a new round must observe new Task State; read failure must
// abort assembly. An isolated Worker must never read Conversation-owned state.
func TestTurnContextReloadsTaskStatePerAssembly(t *testing.T) {
	for _, isolated := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared", true: "isolated"}[isolated], func(t *testing.T) {
			storage := &contextTaskStateStore{Store: fixturestore.New(), goal: "ORIGINAL_GOAL"}
			runtime := newRuntimeForTest(RuntimeOptions{Store: storage, TaskStates: taskstate.NewService(storage, nil)}, nil)
			snapshot := testRuntimeSnapshot()
			snapshot.ContextAssembly = contextassembly.DefaultConfig()
			snapshot.ContextAssembly.HistoryRetrievalEnabled = false
			ctx, _ := runtime.contextSessions.withSession(t.Context(), turn.Request{ConversationID: "conversation", Input: "Continue", Context: turn.Context{Isolated: isolated}}, &snapshot)
			assemble := func() (string, error) {
				pack, err := contextassembly.Assemble(ctx, contextassembly.Request{Messages: []contextassembly.Message{{Role: "user", Content: "Continue"}}})
				var text strings.Builder
				for _, message := range pack.Messages {
					text.WriteString(message.Content)
				}
				return text.String(), err
			}
			for _, goal := range []string{"ORIGINAL_GOAL", "UPDATED_GOAL"} {
				storage.goal = goal
				text, err := assemble()
				if err != nil || strings.Contains(text, goal) == isolated {
					t.Fatalf("isolated=%t goal=%s text=%q error=%v", isolated, goal, text, err)
				}
			}
			storage.err = errors.New("Task State read unavailable")
			_, err := assemble()
			if isolated {
				if err != nil || storage.reads != 0 {
					t.Fatalf("isolated Worker read shared state: reads=%d error=%v", storage.reads, err)
				}
			} else if !errors.Is(err, storage.err) || storage.reads != 3 {
				t.Fatalf("round-local state/read error lost: reads=%d error=%v", storage.reads, err)
			}
		})
	}
}
