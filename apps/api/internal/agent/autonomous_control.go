package agent

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (r *Runtime) heartbeatRun(runID string) error {
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	_, err := r.store.UpdateRunHeartbeat(runID)
	return err
}

func (r *Runtime) stopIfCanceled(events chan<- domain.RunEvent, runID string) (bool, error) {
	run, ok, err := r.store.GetRun(runID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if run.Status != domain.RunCanceling && run.Status != domain.RunCanceled {
		return false, nil
	}
	if run.Status == domain.RunCanceling {
		run, err = r.store.UpdateRunStatus(run.ID, domain.RunCanceled, "canceled by user")
		if err != nil {
			return false, err
		}
	}
	log.Printf("autonomous_canceled run_id=%s", run.ID)
	r.publishRunLifecycle(context.Background(), run, domain.EventRunCanceled, map[string]any{"status": run.Status})
	events <- liveRunEvent(run)
	return true, errRunCanceled
}

func limitStopReason(limits AutonomousLimits, startedAt time.Time, outputChars int, toolCalls int, enforceLegacyResourceLimits bool) string {
	if enforceLegacyResourceLimits && time.Since(startedAt) >= limits.MaxRuntime {
		return "max_runtime reached"
	}
	if outputChars >= limits.MaxOutputChars {
		return "max_output_chars reached"
	}
	if enforceLegacyResourceLimits && toolCalls >= limits.MaxToolCalls {
		return "max_tool_calls reached"
	}
	return ""
}

func (r *Runtime) finishAutonomous(ctx context.Context, events chan<- domain.RunEvent, prepared PreparedCollaborationRun, iteration int, task string, lastAct string, lastReview string, reason string) error {
	return r.emitAutonomousFinal(ctx, events, prepared, iteration, reason, formatAutonomousFallbackFinal(task, lastAct, lastReview, reason))
}

func (r *Runtime) emitAutonomousFinal(ctx context.Context, events chan<- domain.RunEvent, prepared PreparedCollaborationRun, iteration int, reason string, finalAnswer string) error {
	output := strings.TrimSpace(finalAnswer)
	if output == "" {
		output = "Autonomous run stopped without a final answer."
	}
	step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: prepared.Run.ConversationID,
		Role:           "final",
		AgentID:        prepared.WorkerAgent.ID,
		Status:         domain.CollaborationStepCompleted,
		Iteration:      iteration,
		Input:          "Stop reason: " + strings.TrimSpace(reason),
		Output:         output,
	})
	if err != nil {
		return err
	}
	log.Printf("autonomous_final run_id=%s iteration=%d reason=%q output_len=%d", prepared.Run.ID, iteration, reason, len(output))
	r.publishStage(ctx, step, domain.EventStageCompleted)
	events <- liveStageEvent(step)
	emitFinalDeltas(ctx, prepared.Run.ID, output, events)
	return nil
}

func parseAutonomousDecision(value string) autonomousDecision {
	jsonText, err := extractJSONObject(value)
	if err != nil {
		return autonomousDecision{}
	}
	var decoded autonomousDecision
	if err := json.Unmarshal([]byte(jsonText), &decoded); err != nil {
		return autonomousDecision{}
	}
	decoded.ValidJSON = true
	decoded.Decision = strings.ToLower(strings.TrimSpace(decoded.Decision))
	if decoded.Decision != "continue" && decoded.Decision != "stop" && decoded.Decision != "rework" && decoded.Decision != "ask_user" {
		decoded.Decision = ""
	}
	if decoded.Decision == "rework" {
		decoded.Decision = "continue"
	}
	decoded.Reason = strings.TrimSpace(decoded.Reason)
	decoded.Question = strings.TrimSpace(decoded.Question)
	decoded.FinalAnswer = strings.TrimSpace(decoded.FinalAnswer)
	return decoded
}

func inferHumanInputNeed(observe string, plan string, act string, review string, decide string) humanInputNeed {
	sections := []struct {
		source string
		text   string
	}{
		{source: "review", text: review},
		{source: "plan", text: plan},
		{source: "act", text: act},
		{source: "observe", text: observe},
		{source: "decide", text: decide},
	}
	for _, section := range sections {
		if evidence, ok := humanInputEvidence(section.text); ok {
			return humanInputNeed{
				Needed:   true,
				Source:   section.source,
				Evidence: evidence,
				Question: humanInputQuestion(evidence),
			}
		}
	}
	return humanInputNeed{}
}

func humanInputEvidence(value string) (string, bool) {
	for _, line := range strings.Split(value, "\n") {
		line = cleanHumanInputEvidence(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if containsAny(lower, humanInputNegativeMarkers()) {
			continue
		}
		if containsAny(lower, humanInputPositiveMarkers()) {
			return truncateAutonomousText(line, 220), true
		}
	}
	return "", false
}

func cleanHumanInputEvidence(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "-*•0123456789.、) \t")
	return strings.TrimSpace(value)
}

func humanInputQuestion(evidence string) string {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return "Please provide the missing information needed to continue."
	}
	if strings.Contains(evidence, "?") || strings.Contains(evidence, "？") {
		return evidence
	}
	if strings.Contains(evidence, "用户") || strings.Contains(evidence, "需要") || strings.Contains(evidence, "缺少") {
		return "请补充继续完成任务所需的信息：" + evidence
	}
	return "Please provide the missing information needed to continue: " + evidence
}

func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func truncateAutonomousText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func humanInputPositiveMarkers() []string {
	return []string{
		"need user input",
		"needs user input",
		"require user input",
		"requires user input",
		"ask the user",
		"ask user",
		"user must provide",
		"user needs to provide",
		"need the user to provide",
		"requires clarification",
		"require clarification",
		"need clarification from the user",
		"missing information",
		"missing user information",
		"cannot proceed without",
		"can't proceed without",
		"cannot continue without",
		"can't continue without",
		"blocked until user",
		"waiting for user",
		"need more information from the user",
		"需要用户输入",
		"需要用户提供",
		"需要用户补充",
		"需要用户确认",
		"需要用户澄清",
		"需要进一步澄清",
		"需要补充信息",
		"缺少用户",
		"缺少必要信息",
		"无法继续",
		"不能继续",
		"等待用户",
		"请用户提供",
		"请用户补充",
		"请补充",
	}
}

func humanInputNegativeMarkers() []string {
	return []string{
		"no user input",
		"no additional user input",
		"does not require user input",
		"doesn't require user input",
		"user input is not required",
		"无需用户",
		"不需要用户",
		"无需补充",
		"不需要补充",
		"没有缺少",
	}
}

func (r *Runtime) emitAutonomousProgress(events chan<- domain.RunEvent, runID string, limits AutonomousLimits, startedAt time.Time, iteration int, outputChars int, toolCalls int, stopReason string) {
	progress := AutonomousProgress{
		Iteration: iteration, MaxIterations: limits.MaxIterations,
		ElapsedSeconds: int(time.Since(startedAt).Seconds()), MaxRuntimeSeconds: int(limits.MaxRuntime.Seconds()),
		OutputChars: outputChars, MaxOutputChars: limits.MaxOutputChars,
		ToolCalls: toolCalls, MaxToolCalls: limits.MaxToolCalls, StopReason: strings.TrimSpace(stopReason),
	}
	if run, ok, _ := r.store.GetRun(runID); ok {
		r.publishRunLifecycle(context.Background(), run, domain.EventRunProgress, map[string]any{
			"mode": ChatModeAutonomous, "iteration": progress.Iteration, "max_iterations": progress.MaxIterations,
			"elapsed_seconds": progress.ElapsedSeconds, "max_runtime_seconds": progress.MaxRuntimeSeconds,
			"output_chars": progress.OutputChars, "max_output_chars": progress.MaxOutputChars,
			"tool_calls": progress.ToolCalls, "max_tool_calls": progress.MaxToolCalls, "stop_reason": progress.StopReason,
		})
	}
	events <- domain.RunEvent{Type: domain.EventRunProgress, SchemaVersion: domain.CurrentRunEventSchemaVersion, RunID: runID, Payload: map[string]any{
		"mode": ChatModeAutonomous, "iteration": progress.Iteration, "max_iterations": progress.MaxIterations,
		"elapsed_seconds": progress.ElapsedSeconds, "max_runtime_seconds": progress.MaxRuntimeSeconds,
		"output_chars": progress.OutputChars, "max_output_chars": progress.MaxOutputChars,
		"tool_calls": progress.ToolCalls, "max_tool_calls": progress.MaxToolCalls, "stop_reason": progress.StopReason,
	}}
}
