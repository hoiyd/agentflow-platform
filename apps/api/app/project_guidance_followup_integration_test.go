package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
)

// A follow-up inherits selection, not the old body. Exercise real inbox dispatch
// and fresh Run creation after the operator has changed the project conventions.
func exerciseProjectGuidanceFollowup(t *testing.T, call func(string, any) string, storage *store.PostgresStore, workspace, agent string, previous domain.Run) domain.Run {
	t.Helper()
	response := call("/api/conversations/"+previous.ConversationID+"/inputs", domain.RunInputRequest{
		Kind: "follow_up", RunID: previous.ID, AgentID: agent, Mode: "single",
		Content:        "guidance-gate: summarize the supplied project report project=alpha",
		IdempotencyKey: "project-followup",
	})
	var receipt domain.RunInput
	if err := json.Unmarshal([]byte(response), &receipt); err != nil || receipt.ID == "" {
		t.Fatalf("follow-up rejected: %s %v", response, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := storage.ListRunInputs(ctx, identity.SuperUserID, workspace, previous.ConversationID)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID != receipt.ID || item.AppliedRunID == "" {
				continue
			}
			run, found, err := storage.GetRun(item.AppliedRunID)
			if err != nil || !found || run.Status == domain.RunFailed {
				t.Fatalf("follow-up did not execute: %+v %v", run, err)
			}
			if run.Status != domain.RunCompleted {
				continue
			}
			frozen := run.RuntimeSnapshot.ProjectGuidance
			if run.ID == previous.ID || frozen == nil || frozen.Selection.Root != "alpha" || len(frozen.Selection.Paths) != 1 || frozen.Selection.Paths[0] != "reports/input.md" || frozen.Entries[1].Content != "ALPHA_REPORT_V2" {
				t.Fatal("follow-up lost project selection or reused the old guidance body")
			}
			messages, err := storage.ListMessages(previous.ConversationID)
			if err != nil || len(messages) == 0 || !strings.Contains(messages[len(messages)-1].Content, "ALPHA_REPORT_V2") {
				t.Fatal("follow-up provider did not receive the updated guidance", err)
			}
			return run
		}
		select {
		case <-ctx.Done():
			t.Fatal("follow-up did not complete within its bounded deadline")
		case <-ticker.C:
		}
	}
}
