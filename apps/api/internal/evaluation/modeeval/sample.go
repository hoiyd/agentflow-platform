package modeeval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (h *host) sample(parent context.Context, data Dataset, opts Options, sample *Sample) error {
	started, before := time.Now(), h.guard.count()
	ctx, cancel := context.WithTimeout(parent, time.Duration(opts.TimeoutMS)*time.Millisecond)
	defer cancel()
	conversation, err := h.storage.CreateConversationInWorkspace(h.workspace, "Orchestration evaluation")
	if err != nil {
		return err
	}
	sample.ConversationID = conversation.ID
	commandErr := h.request(ctx, http.MethodPost, "/api/chat", domain.ChatRequest{ConversationID: conversation.ID, AgentID: h.agentID, Message: data.Task, Mode: sample.Mode}, nil)
	// Read the accepted identity from persistence, not a successful SSE response.
	// A disconnected or failed observer must not erase an accepted Run.
	runs, err := h.storage.ListRunsByWorkspace(h.workspace)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.ConversationID == conversation.ID {
			sample.RunID = run.ID
		}
	}
	if sample.RunID == "" {
		sample.Status = "failed"
		sample.StopReason = "run_not_accepted"
		sample.Findings = append(sample.Findings, "chat_command_failed")
		sample.PhysicalRequests = h.guard.count() - before
		sample.WallMS = time.Since(started).Milliseconds()
		return nil
	}
	run, err := h.wait(ctx, sample.RunID)
	if err != nil {
		commandErr = err
	}
	if err == nil && run.Status == domain.RunWaitingForUser && sample.Mode == "multi_agent" {
		steps, e := h.storage.ListCollaborationSteps(run.ID)
		if e != nil {
			return e
		}
		plan := ""
		for _, step := range steps {
			if step.Role == "planner" {
				plan = step.Output
			}
		}
		if plan != "" {
			approved := plan
			if opts.ApprovalPolicy == "edit" {
				approved += "\n" + data.ApprovalEdit
			}
			approval := Approval{Action: opts.ApprovalPolicy, OriginalHash: contentHash(plan), SubmittedHash: contentHash(approved), WaitMS: max(0, time.Since(run.UpdatedAt).Milliseconds())}
			if opts.ApprovalPolicy == "reject" {
				approval.SubmittedHash = ""
				commandErr = h.request(ctx, http.MethodPost, "/api/runs/"+run.ID+"/cancel", nil, nil)
			} else {
				commandErr = h.request(ctx, http.MethodPost, "/api/runs/"+run.ID+"/continue", map[string]any{"plan": approved, "routing_requirements": map[string]any{"preferred_capabilities": []string{"evidence_report"}}}, nil)
			}
			sample.Approvals = append(sample.Approvals, approval)
			sample.ApprovalWaitMS = approval.WaitMS
			run, err = h.wait(ctx, run.ID)
			if err != nil {
				commandErr = err
			}
		}
	}
	// The runtime owns accepted work. Cancel explicitly on the evaluator deadline
	// and wait for durable settlement before another arm can spend suite tokens.
	readCtx, readCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer readCancel()
	if ctx.Err() != nil {
		if err = h.request(readCtx, http.MethodPost, "/api/runs/"+sample.RunID+"/cancel", nil, nil); err != nil {
			return fmt.Errorf("cancel timed-out evaluation Run: %w", err)
		}
		if _, err = h.wait(readCtx, sample.RunID); err != nil {
			return err
		}
	}
	if err = h.request(readCtx, http.MethodGet, "/api/runs/"+sample.RunID+"/replay", nil, &sample.Replay); err != nil {
		return err
	}
	run = sample.Replay.Run
	sample.Status = string(run.Status)
	sample.Usage = sample.Replay.UsageLedger.Totals
	sample.ActiveMS = run.ActiveRuntimeMS
	sample.WallMS = time.Since(started).Milliseconds()
	sample.PhysicalRequests = h.guard.count() - before
	sample.StopReason = string(run.Status)
	if commandErr != nil {
		sample.Findings = append(sample.Findings, "command_or_observer_error")
	}
	if sample.Usage.CostUnknownEntries == 0 && sample.Usage.OpenReservations == 0 {
		cost := sample.Usage.EstimatedCostMicros
		sample.CostMicros = &cost
	}
	for _, step := range sample.Replay.Steps {
		sample.StageMS[step.Role] += step.UpdatedAt.Sub(step.CreatedAt).Milliseconds()
		sample.Iterations = max(sample.Iterations, step.Iteration)
		if step.Role == "final" {
			sample.StopReason = step.Input
		}
	}
	for _, event := range sample.Replay.RunEvents {
		if sample.Mode != "autonomous" && event.Payload["stop_reason"] != nil {
			if reason, ok := event.Payload["stop_reason"].(string); ok && reason != "" {
				sample.StopReason = reason
			}
		}
	}
	stored, found, err := h.storage.GetRun(sample.RunID)
	if err != nil {
		return fmt.Errorf("load persisted evaluation snapshot: %w", err)
	}
	if !found {
		return fmt.Errorf("persisted evaluation Run disappeared")
	}
	sample.BoundaryHash = commonBoundary(stored.RuntimeSnapshot, data, opts)
	for _, step := range sample.Replay.Steps {
		if (step.Role == "worker" || step.Role == "act") && step.AgentID != h.agentID {
			sample.BoundaryHash = ""
			sample.Findings = append(sample.Findings, "selected_worker_drift")
		}
	}
	current, err := os.ReadFile(filepath.Join(h.directory, "source", "brief.md"))
	if err != nil || string(current) != data.Material {
		sample.BoundaryHash = ""
		sample.ConstraintViolations++
		sample.Findings = append(sample.Findings, "material_changed")
	}
	score(data, sample)
	return nil
}

func commonBoundary(snapshot *domain.RuntimeSnapshot, data Dataset, opts Options) string {
	if snapshot == nil {
		return ""
	}
	// Everything except the declared bundle must match, including auxiliary Tool
	// definitions. Never hand-pick a subset that could conceal capability drift.
	common := *snapshot
	common.Mode = ""
	common.CreatedAt = time.Time{}
	common.CandidateAgents = nil
	common.AutonomousLimits = nil
	return digest(struct {
		Snapshot                         domain.RuntimeSnapshot
		Material, Task, Approval, Sensor string
	}{common, contentHash(data.Material), data.Task, opts.ApprovalPolicy, "sourced-json-and-durable-read-v1"})
}

func (h *host) wait(ctx context.Context, id string) (domain.Run, error) {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, found, err := h.storage.GetRun(id)
		if err != nil {
			return run, err
		}
		if !found {
			return run, fmt.Errorf("evaluation Run not found")
		}
		switch run.Status {
		case domain.RunCompleted, domain.RunFailed, domain.RunFailedRecoverable, domain.RunCanceled, domain.RunWaitingForUser:
			// Uncertain provider failures can intentionally retain reservations.
			// Preserve them and stop suite admission; waiting cannot settle them.
			return run, nil
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (h *host) request(ctx context.Context, method, path string, input, output any) error {
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, h.api.URL+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("X-Workspace-ID", h.workspace)
	req.Header.Set("Content-Type", "application/json")
	response, err := h.api.Client().Do(req)
	if err != nil {
		return fmt.Errorf("evaluation HTTP command interrupted")
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("evaluation HTTP %s %s status=%d", method, path, response.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(output)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}
