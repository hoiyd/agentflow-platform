package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

func TestSelectWorkerAgentChoosesCodingForImplementationTask(t *testing.T) {
	agents := testAgents()

	decision := selectWorkerAgent(agents, "修复前端 React 组件里的 bug，并补充 Go API 测试", "1. Inspect frontend state\n2. Patch backend API\n3. Run tests")

	if decision.Agent.ID != "agent_coding" {
		t.Fatalf("expected coding agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
	if !strings.Contains(decision.Output(), "Candidate scores") {
		t.Fatalf("expected transparent candidate scores, got:\n%s", decision.Output())
	}
}

func TestSelectWorkerAgentChoosesResearchForMarketTask(t *testing.T) {
	agents := testAgents()

	decision := selectWorkerAgent(agents, "Compare competitors and verify recent pricing sources for this product launch", "1. Gather sources\n2. Compare market positioning")

	if decision.Agent.ID != "agent_research" {
		t.Fatalf("expected research agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
}

func TestSelectWorkerAgentChoosesDataForBudgetTask(t *testing.T) {
	agents := testAgents()

	decision := selectWorkerAgent(agents, "计算下个季度预算、成本和 capacity tradeoff", "1. Estimate cost\n2. Compare budget scenarios")

	if decision.Agent.ID != "agent_data" {
		t.Fatalf("expected data agent, got %s with output:\n%s", decision.Agent.ID, decision.Output())
	}
}

func TestParseLLMRouteDecision(t *testing.T) {
	agents := testAgents()

	decision, err := parseLLMRouteDecision(`{
		"agent_id": "agent_research",
		"reason": "The task depends on market context and external source comparison.",
		"confidence": 0.82,
		"scores": [
			{"agent_id": "agent_research", "score": 91, "reason": "Best fit for source comparison."},
			{"agent_id": "agent_coding", "score": 22, "reason": "Some implementation detail, but not primary."}
		]
	}`, agents)
	if err != nil {
		t.Fatalf("parse llm route decision: %v", err)
	}
	if decision.Agent.ID != "agent_research" {
		t.Fatalf("expected research agent, got %s", decision.Agent.ID)
	}
	if decision.Mode != "llm" || decision.Confidence != 0.82 {
		t.Fatalf("expected llm mode and confidence, got %#v", decision)
	}
	if len(decision.Scores) != len(agents) {
		t.Fatalf("expected one score per agent after fill, got %d", len(decision.Scores))
	}
}

func TestParseLLMRouteDecisionRejectsUnknownAgent(t *testing.T) {
	_, err := parseLLMRouteDecision(`{"agent_id":"agent_missing","reason":"bad","scores":[]}`, testAgents())
	if err == nil {
		t.Fatal("expected unknown agent error")
	}
}

func TestParseAutonomousDecision(t *testing.T) {
	decision := parseAutonomousDecision(`{"decision":"stop","reason":"done","final_answer":"Complete."}`)
	if !decision.ValidJSON {
		t.Fatal("expected valid decision JSON")
	}
	if decision.Decision != "stop" || decision.Reason != "done" || decision.FinalAnswer != "Complete." {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestParseAutonomousDecisionAskUser(t *testing.T) {
	decision := parseAutonomousDecision(`{"decision":"ask_user","reason":"missing project name","question":"What project should this status update describe?","final_answer":""}`)
	if !decision.ValidJSON {
		t.Fatal("expected valid decision JSON")
	}
	if decision.Decision != "ask_user" || decision.Question == "" {
		t.Fatalf("unexpected ask_user decision: %#v", decision)
	}
}

func TestInferHumanInputNeedFromPlan(t *testing.T) {
	need := inferHumanInputNeed(
		"Task is understood.",
		"Plan:\n1. Cannot proceed without user input about the target customer segment before drafting the launch plan.",
		"Not started.",
		"Review pending.",
		`{"decision":"stop","reason":"done","final_answer":"Complete."}`,
	)
	if !need.Needed {
		t.Fatal("expected human input need")
	}
	if need.Source != "plan" {
		t.Fatalf("expected plan source, got %q", need.Source)
	}
	if !strings.Contains(need.Question, "target customer segment") {
		t.Fatalf("expected generated question to include evidence, got %q", need.Question)
	}
}

func TestInferHumanInputNeedFromChineseReview(t *testing.T) {
	need := inferHumanInputNeed(
		"任务已理解。",
		"先整理已有信息。",
		"已完成初稿。",
		"Review:\n- 需要用户补充目标受众，否则无法继续完善文案。",
		`{"decision":"continue","reason":"next iteration"}`,
	)
	if !need.Needed {
		t.Fatal("expected human input need")
	}
	if need.Source != "review" {
		t.Fatalf("expected review source, got %q", need.Source)
	}
	if !strings.Contains(need.Question, "目标受众") {
		t.Fatalf("expected generated question to include Chinese evidence, got %q", need.Question)
	}
}

func TestInferHumanInputNeedIgnoresNegativeStatement(t *testing.T) {
	need := inferHumanInputNeed(
		"Task is understood.",
		"No additional user input is required; continue with the given constraints.",
		"Draft is complete.",
		"Review: no blocking gaps.",
		`{"decision":"stop","reason":"done","final_answer":"Complete."}`,
	)
	if need.Needed {
		t.Fatalf("did not expect human input need: %#v", need)
	}
}

func TestParseAutonomousDecisionRejectsBadJSON(t *testing.T) {
	decision := parseAutonomousDecision("not json")
	if decision.ValidJSON || decision.Decision != "" {
		t.Fatalf("expected invalid empty decision, got %#v", decision)
	}
}

func TestAutonomousRunStopsAtMaxIterations(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Autonomous test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntimeWithRouterModeAndLimits(fileStore, openai.NewClientWithTimeout("", "", "test", time.Second), nil, RouterModeQuery, AutonomousLimits{
		MaxIterations:  1,
		MaxRuntime:     time.Minute,
		MaxOutputChars: 60000,
		MaxToolCalls:   20,
	})
	prepared, err := runtime.PrepareAutonomousRun(context.Background(), conversation.ID)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}

	events, errs := runtime.RunAutonomous(context.Background(), prepared, "Write a concise project update.")
	seenProgress := false
	for event := range events {
		if event.Type == "autonomous_progress" {
			seenProgress = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("run autonomous: %v", err)
	}
	if !seenProgress {
		t.Fatal("expected autonomous progress event")
	}

	steps, err := fileStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected autonomous steps")
	}
	if steps[0].Iteration != 1 {
		t.Fatalf("expected first step iteration 1, got %d", steps[0].Iteration)
	}
	if steps[len(steps)-1].Role != "final" {
		t.Fatalf("expected final step, got %q", steps[len(steps)-1].Role)
	}
}

func TestAutonomousRunCanBeCanceledBeforeLoop(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Cancel test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntimeWithRouterModeAndLimits(fileStore, openai.NewClientWithTimeout("", "", "test", time.Second), nil, RouterModeQuery, AutonomousLimits{
		MaxIterations:  2,
		MaxRuntime:     time.Minute,
		MaxOutputChars: 60000,
		MaxToolCalls:   20,
	})
	prepared, err := runtime.PrepareAutonomousRun(context.Background(), conversation.ID)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	if _, err := runtime.CancelRun(prepared.Run.ID); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	events, errs := runtime.RunAutonomous(context.Background(), prepared, "Long task")
	seenCanceled := false
	for event := range events {
		if event.Type == "run" && event.Run.Status == domain.RunCanceled {
			seenCanceled = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("expected clean cancel, got %v", err)
	}
	if !seenCanceled {
		t.Fatal("expected canceled run event")
	}
	run, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || run.Status != domain.RunCanceled {
		t.Fatalf("expected canceled run, got %#v", run)
	}
}

func TestResumeAutonomousCompletesHumanInputCheckpoint(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("HITL test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	runtime := NewRuntimeWithRouterModeAndLimits(fileStore, openai.NewClientWithTimeout("", "", "test", time.Second), nil, RouterModeQuery, AutonomousLimits{
		MaxIterations:  2,
		MaxRuntime:     time.Minute,
		MaxOutputChars: 60000,
		MaxToolCalls:   20,
	})
	prepared, err := runtime.PrepareAutonomousRun(context.Background(), conversation.ID)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}
	checkpoint, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: conversation.ID,
		Role:           "human_input",
		AgentID:        prepared.WorkerAgent.ID,
		Status:         domain.CollaborationStepRunning,
		Iteration:      1,
		Input:          "missing project",
		Output:         "Which project?",
	})
	if err != nil {
		t.Fatalf("create checkpoint: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, ""); err != nil {
		t.Fatalf("mark waiting: %v", err)
	}

	events, errs := runtime.ResumeAutonomous(context.Background(), prepared.Run.ID, "AgentFlow")
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatalf("resume autonomous: %v", err)
	}
	updated, ok, err := fileStore.GetRun(prepared.Run.ID)
	if err != nil || !ok {
		t.Fatalf("get run after resume: %v", err)
	}
	if updated.Status == domain.RunWaitingForUser {
		t.Fatalf("expected run to leave waiting_for_user")
	}
	steps, err := fileStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	foundCompletedCheckpoint := false
	for _, step := range steps {
		if step.ID == checkpoint.ID && step.Status == domain.CollaborationStepCompleted && step.Output == "AgentFlow" {
			foundCompletedCheckpoint = true
		}
	}
	if !foundCompletedCheckpoint {
		t.Fatal("expected completed human input checkpoint")
	}
}

func testAgents() []domain.Agent {
	now := time.Now().UTC()
	return []domain.Agent{
		{
			ID:           "agent_research",
			Name:         "Field Researcher",
			Description:  "Investigates people, places, products, and market context, then separates verified facts from open questions.",
			SystemPrompt: "Gather research context with search and compare sources carefully.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_coding",
			Name:         "Systems Builder",
			Description:  "Turns implementation requests into concrete technical steps, debugging hypotheses, and maintainable code changes.",
			SystemPrompt: "Focus on software behavior, interfaces, edge cases, and implementation tradeoffs.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_data",
			Name:         "Operations Analyst",
			Description:  "Evaluates budgets, schedules, capacity, and tradeoffs with explicit assumptions and calculation-backed reasoning.",
			SystemPrompt: "Treat questions as operational decisions involving cost, time, capacity, or prioritization.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
		{
			ID:           "agent_planner",
			Name:         "Narrative Strategist",
			Description:  "Shapes messy goals into audience-aware briefs, storylines, launch plans, and decision-ready next actions.",
			SystemPrompt: "Clarify the audience, intent, and constraints behind a request.",
			CreatedAt:    now,
			UpdatedAt:    now,
		},
	}
}
