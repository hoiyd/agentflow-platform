package runcommands

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Rejections must happen before conversation/message creation; capacity must
// be returned on every path. Execution/Stop/disconnect use production gates.
func TestStartRejectsBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  domain.ChatRequest
		full bool
	}{
		{name: "empty message"},
		{name: "scope mismatch", req: domain.ChatRequest{WorkspaceID: "2", Message: "task"}},
		{name: "foreign agent", req: domain.ChatRequest{AgentID: "missing", Message: "task"}},
		{name: "overload", req: domain.ChatRequest{Message: "task"}, full: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := fixturestore.New()
			controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})
			if tc.full {
				reservation, err := controller.Reserve()
				if err != nil {
					t.Fatal(err)
				}
				defer reservation.Cancel()
			}
			commands := Commands{Store: storage, Capacity: controller}
			err := commands.Start(context.Background(), Scope{Workspace: domain.NewWorkspaceScope("1"), Owner: "super"}, tc.req, nil, nil, Observer{})
			if err == nil {
				t.Fatal("invalid command accepted")
			}
			if tc.full && !errors.Is(err, concurrency.ErrQueueFull) {
				t.Fatalf("lost capacity error: %v", err)
			}
			conversations, err := storage.ListConversations()
			if err != nil || len(conversations) != 0 {
				t.Fatalf("rejection wrote conversations: %v %v", conversations, err)
			}
			if !tc.full {
				reservation, err := controller.Reserve()
				if err != nil {
					t.Fatalf("capacity leaked: %v", err)
				}
				reservation.Cancel()
			}
		})
	}
}
