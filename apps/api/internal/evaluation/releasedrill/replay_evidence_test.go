package releasedrill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

type replay struct {
	Run struct {
		Status string `json:"status"`
	} `json:"run"`
	RunEvents []struct {
		Type string `json:"type"`
	} `json:"run_events"`
	StageCheckpoints []struct {
		Status string `json:"status"`
	} `json:"stage_checkpoints"`
	RecoverySummary map[string]any `json:"recovery_summary"`
}

func (d *drill) replay(ctx context.Context, runID string) (replay, error) {
	status, body, err := d.request(ctx, http.MethodGet, "/api/runs/"+runID+"/replay", "")
	if err != nil {
		return replay{}, err
	}
	if status != http.StatusOK {
		return replay{}, fmt.Errorf("get replay: status=%d body=%s", status, safeText(string(body)))
	}
	var result replay
	if err := json.Unmarshal(body, &result); err != nil {
		return replay{}, err
	}
	return result, nil
}

func validateRecoverableReplay(value replay) error {
	if value.Run.Status != "failed_recoverable" {
		return fmt.Errorf("status=%q, want failed_recoverable", value.Run.Status)
	}
	if len(value.StageCheckpoints) == 0 || value.RecoverySummary == nil {
		return errors.New("missing Stage checkpoint or recovery summary")
	}
	if !contains(eventTypes(value), "run.failed") || !contains(eventTypes(value), "stage.failed") {
		return errors.New("missing repaired terminal events")
	}
	return nil
}

func validateCompletedReplay(value replay) error {
	if value.Run.Status != "completed" {
		return fmt.Errorf("status=%q, want completed", value.Run.Status)
	}
	events := eventTypes(value)
	if !contains(events, "run.resumed") || !contains(events, "checkpoint.compensation_completed") || !contains(events, "run.completed") {
		return errors.New("missing Resume, compensation, or completion event")
	}
	return nil
}

func eventTypes(value replay) []string {
	result := make([]string, 0, len(value.RunEvents))
	for _, event := range value.RunEvents {
		result = append(result, event.Type)
	}
	return result
}

func checkpointStatuses(value replay) []string {
	result := make([]string, 0, len(value.StageCheckpoints))
	for _, checkpoint := range value.StageCheckpoints {
		result = append(result, checkpoint.Status)
	}
	return result
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestReplayEvidenceValidation(t *testing.T) {
	recoverable := replay{}
	recoverable.Run.Status = "failed_recoverable"
	recoverable.StageCheckpoints = append(recoverable.StageCheckpoints, struct {
		Status string `json:"status"`
	}{Status: "executing"})
	recoverable.RecoverySummary = map[string]any{"reason": "run_recoverable"}
	recoverable.RunEvents = append(recoverable.RunEvents,
		struct {
			Type string `json:"type"`
		}{Type: "stage.failed"},
		struct {
			Type string `json:"type"`
		}{Type: "run.failed"},
	)
	if err := validateRecoverableReplay(recoverable); err != nil {
		t.Fatalf("valid recoverable Replay rejected: %v", err)
	}
	recoverable.StageCheckpoints = nil
	if err := validateRecoverableReplay(recoverable); err == nil {
		t.Fatal("missing checkpoint evidence accepted")
	}

	completed := replay{}
	completed.Run.Status = "completed"
	for _, eventType := range []string{"run.resumed", "checkpoint.compensation_completed", "run.completed"} {
		completed.RunEvents = append(completed.RunEvents, struct {
			Type string `json:"type"`
		}{Type: eventType})
	}
	if err := validateCompletedReplay(completed); err != nil {
		t.Fatalf("valid completed Replay rejected: %v", err)
	}
	completed.RunEvents = completed.RunEvents[:2]
	if err := validateCompletedReplay(completed); err == nil {
		t.Fatal("incomplete Resume evidence accepted")
	}
}
