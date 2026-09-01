package event

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRecorderToolArtifactHandlesOptionalAndFailurePaths(t *testing.T) {
	payload := ToolArtifactPayload{ArtifactID: "artifact-1", Operation: "read"}
	var nilRecorder *Recorder
	nilRecorder.ToolArtifact(context.Background(), domain.EventArtifactRead, payload)
	NewRecorder(nil).ToolArtifact(context.Background(), domain.EventArtifactRead, payload)
	NewRecorder(&artifactEventStoreStub{}).ToolArtifact(nil, domain.EventArtifactRead, payload)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	NewRecorder(&artifactEventStoreStub{}).ToolArtifact(canceled, domain.EventArtifactRead, payload)
	NewRecorder(&artifactEventStoreStub{}).ToolArtifact(context.Background(), domain.EventArtifactRead, payload)

	ctx := WithScope(context.Background(), Scope{RunID: "run-1", ConversationID: "conversation-1"})
	invalid := &artifactEventStoreStub{}
	NewRecorder(invalid).ToolArtifact(ctx, domain.EventRunCreated, payload)
	if invalid.calls != 0 {
		t.Fatalf("invalid artifact event reached store %d time(s)", invalid.calls)
	}

	failing := &artifactEventStoreStub{err: errors.New("store unavailable")}
	NewRecorder(failing).ToolArtifact(ctx, domain.EventArtifactRead, payload)
	if failing.calls != 1 {
		t.Fatalf("failing store calls = %d", failing.calls)
	}

	success := &artifactEventStoreStub{}
	NewRecorder(success).ToolArtifact(ctx, domain.EventArtifactRead, payload)
	if success.calls != 1 || success.item.Type != domain.EventArtifactRead || success.item.RunID != "run-1" {
		t.Fatalf("persisted artifact event = %#v", success.item)
	}
}

type artifactEventStoreStub struct {
	calls int
	item  domain.RunEvent
	err   error
}

func (s *artifactEventStoreStub) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	s.calls++
	s.item = item
	return item, s.err
}
