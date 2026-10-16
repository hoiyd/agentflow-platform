package httpapi

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

type transitioningProjectionWorkspace struct {
	store.WorkspaceStore
	transition func()
}

func (s transitioningProjectionWorkspace) ListRunEvents(id string) ([]domain.RunEvent, error) {
	s.transition()
	return s.WorkspaceStore.ListRunEvents(id)
}

// A transition between the status and event reads must not leave a subscriber
// with an old status and a cursor that already acknowledges its stopping event.
func TestProjectionStatusDoesNotLagEventWatermark(t *testing.T) {
	for _, tc := range []struct {
		status domain.RunStatus
		event  domain.RunEventType
	}{
		{domain.RunCanceled, domain.EventRunCanceled},
		{domain.RunCompleted, domain.EventRunCompleted},
		{domain.RunFailed, domain.EventRunFailed},
		{domain.RunWaitingForUser, domain.EventRunWaitingForUser},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			fixture, run := createHTTPTestRun(t)
			if _, err := fixture.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
				t.Fatal(err)
			}
			for _, typ := range []domain.RunEventType{domain.EventRunCreated, domain.EventRunStarted} {
				if _, err := fixture.CreateRunEvent(domain.RunEvent{Type: typ, RunID: run.ID, ConversationID: run.ConversationID}); err != nil {
					t.Fatal(err)
				}
			}
			var committed domain.RunEvent
			scoped := transitioningProjectionWorkspace{
				WorkspaceStore: fixture.ForWorkspace(domain.NewWorkspaceScope(run.WorkspaceID)),
				transition: func() {
					if tc.status == domain.RunCanceled {
						if _, err := fixture.CreateRunEvent(domain.RunEvent{Type: domain.EventRunCancelRequested, RunID: run.ID, ConversationID: run.ConversationID}); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := fixture.UpdateRunStatus(run.ID, tc.status, ""); err != nil {
						t.Fatal(err)
					}
					var err error
					committed, err = fixture.CreateRunEvent(domain.RunEvent{
						Type: tc.event, RunID: run.ID, ConversationID: run.ConversationID,
						Payload: map[string]any{"status": tc.status},
					})
					if err != nil {
						t.Fatal(err)
					}
				},
			}
			read, found, err := (&Handler{}).loadRunProjection(scoped, run.ID)
			if err != nil || !found {
				t.Fatalf("projection found=%v err=%v", found, err)
			}
			if read.Snapshot.AsOfSequence != committed.Sequence || read.Snapshot.Run.Status != tc.status {
				t.Fatalf("status must include the transition acknowledged by cursor %d: status=%s cursor=%d",
					committed.Sequence, read.Snapshot.Run.Status, read.Snapshot.AsOfSequence)
			}
		})
	}
}
