package modeeval

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (h *host) sample(parent context.Context, data Dataset, opts Options, sample *Sample) error {
	started, before := time.Now(), h.Requests()
	ctx, cancel := context.WithTimeout(parent, time.Duration(opts.TimeoutMS)*time.Millisecond)
	defer cancel()
	conversation, err := h.Storage.CreateConversationInWorkspace(h.Workspace, "Orchestration evaluation")
	if err != nil {
		return err
	}
	sample.ConversationID = conversation.ID
	commandErr := h.Request(ctx, http.MethodPost, "/api/chat", domain.ChatRequest{ConversationID: conversation.ID, AgentID: h.AgentID, Message: data.Task, Mode: sample.Mode}, nil)
	// Read the accepted identity from persistence, not a successful SSE response.
	// A disconnected or failed observer must not erase an accepted Run.
	runs, err := h.Storage.ListRunsByWorkspace(h.Workspace)
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
		sample.PhysicalRequests = h.Requests() - before
		sample.WallMS = time.Since(started).Milliseconds()
		return nil
	}
	run, err := h.Wait(ctx, sample.RunID)
	if err != nil {
		commandErr = err
	}
	if err == nil && run.Status == domain.RunWaitingForUser && sample.Mode == "multi_agent" {
		steps, e := h.Storage.ListCollaborationSteps(run.ID)
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
				commandErr = h.Request(ctx, http.MethodPost, "/api/runs/"+run.ID+"/cancel", nil, nil)
			} else {
				commandErr = h.Request(ctx, http.MethodPost, "/api/runs/"+run.ID+"/continue", map[string]any{"plan": approved, "routing_requirements": map[string]any{"preferred_capabilities": []string{"evidence_report"}}}, nil)
			}
			sample.Approvals = append(sample.Approvals, approval)
			sample.ApprovalWaitMS = approval.WaitMS
			run, err = h.Wait(ctx, run.ID)
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
		if err = h.Request(readCtx, http.MethodPost, "/api/runs/"+sample.RunID+"/cancel", nil, nil); err != nil {
			return fmt.Errorf("cancel timed-out evaluation Run: %w", err)
		}
		if _, err = h.Wait(readCtx, sample.RunID); err != nil {
			return err
		}
	}
	if err = h.Request(readCtx, http.MethodGet, "/api/runs/"+sample.RunID+"/replay", nil, &sample.Replay); err != nil {
		return err
	}
	run = sample.Replay.Run
	sample.Status = string(run.Status)
	sample.Usage = sample.Replay.UsageLedger.Totals
	sample.ActiveMS = run.ActiveRuntimeMS
	sample.WallMS = time.Since(started).Milliseconds()
	sample.PhysicalRequests = h.Requests() - before
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
	stored, found, err := h.Storage.GetRun(sample.RunID)
	if err != nil {
		return fmt.Errorf("load persisted evaluation snapshot: %w", err)
	}
	if !found {
		return fmt.Errorf("persisted evaluation Run disappeared")
	}
	sample.BoundaryHash = commonBoundary(stored.RuntimeSnapshot, data, opts)
	for _, step := range sample.Replay.Steps {
		if (step.Role == "worker" || step.Role == "act") && step.AgentID != h.AgentID {
			sample.BoundaryHash = ""
			sample.Findings = append(sample.Findings, "selected_worker_drift")
		}
	}
	current, err := os.ReadFile(filepath.Join(h.Directory, "source", "brief.md"))
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
