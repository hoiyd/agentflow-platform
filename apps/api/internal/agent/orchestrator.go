package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

const (
	ChatModeSingle     = "single"
	ChatModeMultiAgent = "multi_agent"
	ChatModeAutonomous = "autonomous"
	RouterModeAuto     = "auto"
	RouterModeQuery    = "query_match"
)

type PreparedCollaborationRun struct {
	WorkerAgent domain.Agent
	Run         domain.Run
}

type CollaborationEvent struct {
	Type     string
	Run      domain.Run
	Step     domain.CollaborationStep
	Progress AutonomousProgress
	Delta    string
}

type AutonomousProgress struct {
	Iteration         int
	MaxIterations     int
	ElapsedSeconds    int
	MaxRuntimeSeconds int
	OutputChars       int
	MaxOutputChars    int
	ToolCalls         int
	MaxToolCalls      int
	StopReason        string
}

func (r *Runtime) PrepareCollaborationRun(ctx context.Context, agentID string, conversationID string) (PreparedCollaborationRun, error) {
	agent, err := r.resolveAgent("")
	if err != nil {
		return PreparedCollaborationRun{}, err
	}

	run, err := r.store.CreateRun(agent.ID, conversationID)
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	log.Printf("collaboration_prepare run_id=%s initial_agent_id=%s requested_agent_id=%q", run.ID, agent.ID, strings.TrimSpace(agentID))
	return PreparedCollaborationRun{WorkerAgent: agent, Run: run}, nil
}

func (r *Runtime) RunCollaboration(ctx context.Context, prepared PreparedCollaborationRun, task string) (<-chan CollaborationEvent, <-chan error) {
	events := make(chan CollaborationEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		_, err := r.runCollaborationStep(ctx, events, prepared, "planner", "", plannerPrompt(), task)
		if err != nil {
			errs <- err
			return
		}
		_, err = r.store.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, "")
		if err != nil {
			errs <- err
			return
		}
	}()

	return events, errs
}

func (r *Runtime) ContinueCollaboration(ctx context.Context, runID string, plan string) (<-chan CollaborationEvent, <-chan error) {
	events := make(chan CollaborationEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		run, ok, err := r.store.GetRun(strings.TrimSpace(runID))
		if err != nil {
			errs <- err
			return
		}
		if !ok {
			errs <- store.ErrNotFound("run")
			return
		}
		if run.Status != domain.RunWaitingForUser {
			errs <- fmt.Errorf("run is not waiting for user input")
			return
		}

		agents, err := r.store.ListAgents()
		if err != nil {
			errs <- err
			return
		}
		if len(agents) == 0 {
			errs <- store.ErrNotFound("agent")
			return
		}

		steps, err := r.store.ListCollaborationSteps(run.ID)
		if err != nil {
			errs <- err
			return
		}
		plannerStep, ok := findCollaborationStep(steps, "planner")
		if !ok {
			errs <- fmt.Errorf("planner step not found")
			return
		}
		plan = strings.TrimSpace(plan)
		if plan == "" {
			plan = plannerStep.Output
		}
		task := plannerStep.Input
		updatedPlan, err := r.store.UpdateCollaborationStepOutput(plannerStep.ID, plan)
		if err != nil {
			errs <- err
			return
		}
		events <- CollaborationEvent{Type: "collaboration_step", Step: updatedPlan}

		run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
		if err != nil {
			errs <- err
			return
		}
		route := r.routeWorkerAgent(ctx, run.ID, agents, task, plan)
		routerInput := fmt.Sprintf("User task:\n%s\n\nApproved plan:\n%s\n\nCandidate agents:\n%s", task, plan, formatCandidateAgents(agents))
		routerStep, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
			RunID:          run.ID,
			ConversationID: run.ConversationID,
			Role:           "router",
			AgentID:        route.Agent.ID,
			Status:         domain.CollaborationStepCompleted,
			Input:          routerInput,
			Output:         route.Output(),
		})
		if err != nil {
			errs <- err
			return
		}
		events <- CollaborationEvent{Type: "collaboration_step", Step: routerStep}
		run, err = r.store.UpdateRunAgent(run.ID, route.Agent.ID)
		if err != nil {
			errs <- err
			return
		}
		events <- CollaborationEvent{Type: "run", Run: run}
		log.Printf("collaboration_router_decision run_id=%s router_mode=%s selected_agent_id=%s selected_agent_name=%q score=%d confidence=%.2f reason=%q", run.ID, route.Mode, route.Agent.ID, route.Agent.Name, route.Score, route.Confidence, route.Reason)

		prepared := PreparedCollaborationRun{WorkerAgent: route.Agent, Run: run}

		workerInput := fmt.Sprintf("User task:\n%s\n\nPlanner output:\n%s\n\nRouter-selected worker:\n%s (%s)\nSelection reason: %s", task, plan, route.Agent.Name, route.Agent.ID, route.Reason)
		worker, err := r.runCollaborationStep(ctx, events, prepared, "worker", prepared.WorkerAgent.ID, workerPrompt(prepared.WorkerAgent), workerInput)
		if err != nil {
			errs <- err
			return
		}

		reviewInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s", task, plan, worker)
		review, err := r.runCollaborationStep(ctx, events, prepared, "reviewer", "", reviewerPrompt(), reviewInput)
		if err != nil {
			errs <- err
			return
		}

		finalInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s\n\nReview:\n%s", task, plan, worker, review)
		final, err := r.runCollaborationStep(ctx, events, prepared, "finalizer", "", finalizerPrompt(), finalInput)
		if err != nil {
			errs <- err
			return
		}
		emitFinalDeltas(ctx, final, events)
	}()

	return events, errs
}

func findCollaborationStep(steps []domain.CollaborationStep, role string) (domain.CollaborationStep, bool) {
	for _, step := range steps {
		if step.Role == role {
			return step, true
		}
	}
	return domain.CollaborationStep{}, false
}

type routeDecision struct {
	Agent      domain.Agent
	Mode       string
	Reason     string
	Score      int
	Confidence float64
	Scores     []agentScore
}

type agentScore struct {
	Agent  domain.Agent
	Score  int
	Reason string
}

func (r *Runtime) routeWorkerAgent(ctx context.Context, runID string, agents []domain.Agent, task string, plan string) routeDecision {
	switch r.routerMode {
	case RouterModeQuery:
		log.Printf("router_start run_id=%s router_mode=query_match candidate_count=%d", runID, len(agents))
		return selectWorkerAgentWithLog(runID, agents, task, plan)
	default:
		log.Printf("router_start run_id=%s router_mode=auto candidate_count=%d llm_available=%t", runID, len(agents), r.openAI.HasAPIKey())
		if !r.openAI.HasAPIKey() {
			log.Printf("router_auto_fallback run_id=%s reason=no_openai_api_key fallback_mode=query_match", runID)
			return selectWorkerAgentWithLog(runID, agents, task, plan)
		}
		decision, err := r.routeWorkerAgentWithLLM(ctx, runID, agents, task, plan)
		if err != nil {
			log.Printf("router_auto_fallback run_id=%s reason=%q fallback_mode=query_match", runID, err.Error())
			return selectWorkerAgentWithLog(runID, agents, task, plan)
		}
		logRouteScores(runID, decision)
		return decision
	}
}

func (r *Runtime) routeWorkerAgentWithLLM(ctx context.Context, runID string, agents []domain.Agent, task string, plan string) (routeDecision, error) {
	input := routerUserPrompt(task, plan, agents)
	span := r.trace.LLMStart(ctx, runID, "", map[string]any{
		"role":        "router",
		"system":      routerSystemPrompt(),
		"input":       input,
		"input_chars": len(input),
	})
	completion, err := r.openAI.CompleteTextDetailed(ctx, routerSystemPrompt(), input)
	if err != nil {
		r.trace.Error(ctx, runID, "", map[string]any{
			"source": "llm",
			"role":   "router",
			"error":  err.Error(),
		})
		return routeDecision{}, err
	}
	response := completion.Text
	r.trace.LLMEnd(ctx, span, map[string]any{
		"role":                  "router",
		"model":                 completion.Model,
		"output":                response,
		"output_chars":          len(response),
		"prompt_tokens":         completion.Usage.PromptTokens,
		"completion_tokens":     completion.Usage.CompletionTokens,
		"total_tokens":          completion.Usage.TotalTokens,
		"token_usage_estimated": completion.Usage.Estimated,
	})
	decision, err := parseLLMRouteDecision(response, agents)
	if err != nil {
		r.trace.Error(ctx, runID, "", map[string]any{
			"source": "router",
			"error":  err.Error(),
			"output": response,
		})
		return routeDecision{}, err
	}
	decision.Mode = "llm"
	log.Printf("router_llm_response run_id=%s raw_len=%d selected_agent_id=%s", runID, len(response), decision.Agent.ID)
	return decision, nil
}

func selectWorkerAgent(agents []domain.Agent, task string, plan string) routeDecision {
	return selectWorkerAgentWithLog("", agents, task, plan)
}

func selectWorkerAgentWithLog(runID string, agents []domain.Agent, task string, plan string) routeDecision {
	query := strings.ToLower(task + "\n" + plan)
	scores := make([]agentScore, 0, len(agents))
	for _, agent := range agents {
		score, reason := scoreAgentForTask(agent, query)
		scores = append(scores, agentScore{Agent: agent, Score: score, Reason: reason})
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Score == scores[j].Score {
			return scores[i].Agent.Name < scores[j].Agent.Name
		}
		return scores[i].Score > scores[j].Score
	})
	if len(scores) == 0 {
		return routeDecision{}
	}
	selected := scores[0]
	decision := routeDecision{
		Agent:      selected.Agent,
		Mode:       RouterModeQuery,
		Reason:     selected.Reason,
		Score:      selected.Score,
		Confidence: 0,
		Scores:     scores,
	}
	logRouteScores(runID, decision)
	return decision
}

func scoreAgentForTask(agent domain.Agent, query string) (int, string) {
	profile := strings.ToLower(agent.ID + " " + agent.Name + " " + agent.Description + " " + agent.SystemPrompt + " " + strings.Join(agent.Tools, " "))
	score := 0
	reasons := []string{}
	profileMatches := []string{}

	for _, token := range strings.FieldsFunc(query, func(r rune) bool {
		return r < '0' || (r > '9' && r < 'A') || (r > 'Z' && r < 'a') || r > 'z'
	}) {
		if len(token) < 4 {
			continue
		}
		if strings.Contains(profile, token) {
			score += 1
			profileMatches = append(profileMatches, token)
		}
	}
	if len(profileMatches) > 0 {
		reasons = append(reasons, fmt.Sprintf("profile term overlap +%d: %s", len(profileMatches), strings.Join(profileMatches, ", ")))
	}

	for _, rule := range routingRules() {
		if !strings.Contains(profile, rule.ProfileHint) {
			continue
		}
		matches := matchedKeywords(query, rule.Keywords)
		if len(matches) == 0 {
			continue
		}
		score += rule.Weight * len(matches)
		reasons = append(reasons, fmt.Sprintf("%s matched %s", rule.Label, strings.Join(matches, ", ")))
	}

	if score == 0 {
		score = 1
		reasons = append(reasons, "fallback score from available agent profile")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "agent profile overlaps with task and plan terms")
	}
	return score, strings.Join(reasons, "; ")
}

type routingRule struct {
	Label       string
	ProfileHint string
	Keywords    []string
	Weight      int
}

func routingRules() []routingRule {
	return []routingRule{
		{
			Label:       "software implementation/debugging",
			ProfileHint: "software",
			Keywords:    []string{"code", "coding", "implement", "implementation", "bug", "debug", "frontend", "backend", "api", "test", "typescript", "go", "react", "css", "代码", "实现", "修复", "前端", "后端", "测试", "接口"},
			Weight:      5,
		},
		{
			Label:       "research and external context",
			ProfileHint: "research",
			Keywords:    []string{"research", "market", "compare", "source", "sources", "verify", "news", "place", "product", "pricing", "competitor", "调研", "市场", "比较", "来源", "验证", "新闻", "产品", "价格", "竞品"},
			Weight:      5,
		},
		{
			Label:       "operations and quantitative analysis",
			ProfileHint: "operational",
			Keywords:    []string{"budget", "cost", "capacity", "schedule", "calculate", "calculation", "metric", "forecast", "tradeoff", "operations", "data", "预算", "成本", "容量", "排期", "计算", "指标", "预测", "取舍", "数据"},
			Weight:      5,
		},
		{
			Label:       "strategy, narrative, and planning",
			ProfileHint: "storyline",
			Keywords:    []string{"plan", "brief", "story", "storyline", "launch", "audience", "message", "strategy", "roadmap", "proposal", "decision", "计划", "简报", "故事", "发布", "受众", "策略", "路线图", "方案", "决策"},
			Weight:      5,
		},
	}
}

func matchedKeywords(query string, keywords []string) []string {
	matches := []string{}
	for _, keyword := range keywords {
		if strings.Contains(query, keyword) {
			matches = append(matches, keyword)
		}
	}
	return matches
}

func logRouteScores(runID string, decision routeDecision) {
	for _, score := range decision.Scores {
		log.Printf("router_candidate_score run_id=%s router_mode=%s agent_id=%s agent_name=%q score=%d reason=%q", runID, decision.Mode, score.Agent.ID, score.Agent.Name, score.Score, score.Reason)
	}
	log.Printf("router_selected run_id=%s router_mode=%s agent_id=%s agent_name=%q score=%d confidence=%.2f reason=%q", runID, decision.Mode, decision.Agent.ID, decision.Agent.Name, decision.Score, decision.Confidence, decision.Reason)
}

func (d routeDecision) Output() string {
	lines := []string{
		fmt.Sprintf("Selected worker: %s (`%s`)", d.Agent.Name, d.Agent.ID),
		fmt.Sprintf("Router mode: %s", d.Mode),
	}
	if d.Mode == "llm" {
		lines = append(lines, fmt.Sprintf("Confidence: %.2f", d.Confidence))
	}
	lines = append(lines, fmt.Sprintf("Reason: %s", d.Reason), "", "Candidate scores:")
	for _, score := range d.Scores {
		lines = append(lines, fmt.Sprintf("- %s (`%s`): %d - %s", score.Agent.Name, score.Agent.ID, score.Score, score.Reason))
	}
	return strings.Join(lines, "\n")
}

type llmRouteResponse struct {
	AgentID    string              `json:"agent_id"`
	Reason     string              `json:"reason"`
	Confidence float64             `json:"confidence"`
	Scores     []llmCandidateScore `json:"scores"`
}

type llmCandidateScore struct {
	AgentID string  `json:"agent_id"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
}

func parseLLMRouteDecision(response string, agents []domain.Agent) (routeDecision, error) {
	jsonText, err := extractJSONObject(response)
	if err != nil {
		return routeDecision{}, err
	}
	var decoded llmRouteResponse
	if err := json.Unmarshal([]byte(jsonText), &decoded); err != nil {
		return routeDecision{}, fmt.Errorf("parse router json: %w", err)
	}
	agentByID := map[string]domain.Agent{}
	for _, agent := range agents {
		agentByID[agent.ID] = agent
	}
	selected, ok := agentByID[strings.TrimSpace(decoded.AgentID)]
	if !ok {
		return routeDecision{}, fmt.Errorf("router selected unknown agent_id %q", decoded.AgentID)
	}

	scores := make([]agentScore, 0, len(agents))
	seen := map[string]bool{}
	for _, item := range decoded.Scores {
		agent, ok := agentByID[strings.TrimSpace(item.AgentID)]
		if !ok {
			return routeDecision{}, fmt.Errorf("router scored unknown agent_id %q", item.AgentID)
		}
		score := int(item.Score + 0.5)
		if score < 0 {
			score = 0
		}
		if score > 100 {
			score = 100
		}
		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			reason = "LLM semantic match score"
		}
		scores = append(scores, agentScore{Agent: agent, Score: score, Reason: reason})
		seen[agent.ID] = true
	}
	for _, agent := range agents {
		if !seen[agent.ID] {
			scores = append(scores, agentScore{Agent: agent, Score: 0, Reason: "LLM did not score this candidate"})
		}
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Score == scores[j].Score {
			return scores[i].Agent.Name < scores[j].Agent.Name
		}
		return scores[i].Score > scores[j].Score
	})

	selectedScore := 0
	for _, score := range scores {
		if score.Agent.ID == selected.ID {
			selectedScore = score.Score
			break
		}
	}
	confidence := decoded.Confidence
	if confidence > 1 {
		confidence = confidence / 100
	}
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}
	reason := strings.TrimSpace(decoded.Reason)
	if reason == "" {
		reason = "LLM selected this agent as the best semantic fit."
	}
	return routeDecision{
		Agent:      selected,
		Mode:       "llm",
		Reason:     reason,
		Score:      selectedScore,
		Confidence: confidence,
		Scores:     scores,
	}, nil
}

func extractJSONObject(value string) (string, error) {
	value = strings.TrimSpace(value)
	start := strings.Index(value, "{")
	end := strings.LastIndex(value, "}")
	if start < 0 || end < start {
		return "", fmt.Errorf("router response did not contain a json object")
	}
	return value[start : end+1], nil
}

func formatCandidateAgents(agents []domain.Agent) string {
	lines := make([]string, 0, len(agents))
	for _, agent := range agents {
		lines = append(lines, fmt.Sprintf("- %s (`%s`): %s\n  System prompt: %s\n  Tools: %s", agent.Name, agent.ID, agent.Description, agent.SystemPrompt, strings.Join(agent.Tools, ", ")))
	}
	return strings.Join(lines, "\n")
}

func routerSystemPrompt() string {
	return "You are the Router collaboration role. Select exactly one worker agent for the approved plan. Return only valid JSON with keys: agent_id, reason, confidence, scores. scores must contain one item per candidate with agent_id, score from 0 to 100, and reason. Do not execute the task."
}

func routerUserPrompt(task string, plan string, agents []domain.Agent) string {
	return fmt.Sprintf("User task:\n%s\n\nApproved plan:\n%s\n\nCandidate agents:\n%s\n\nReturn JSON only. The selected agent_id must be one of the candidate ids.", task, plan, formatCandidateAgents(agents))
}

func (r *Runtime) runCollaborationStep(ctx context.Context, events chan<- CollaborationEvent, prepared PreparedCollaborationRun, role string, agentID string, systemPrompt string, input string) (string, error) {
	step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: prepared.Run.ConversationID,
		Role:           role,
		AgentID:        agentID,
		Status:         domain.CollaborationStepRunning,
		Input:          input,
	})
	if err != nil {
		return "", err
	}
	events <- CollaborationEvent{Type: "collaboration_step", Step: step}

	span := r.trace.LLMStart(ctx, prepared.Run.ID, step.ID, map[string]any{
		"role":        role,
		"agent_id":    agentID,
		"system":      systemPrompt,
		"input":       input,
		"input_chars": len(input),
	})
	completion, err := r.openAI.CompleteTextDetailed(ctx, systemPrompt, input)
	if err != nil {
		r.trace.Error(ctx, prepared.Run.ID, step.ID, map[string]any{
			"source":   "llm",
			"role":     role,
			"agent_id": agentID,
			"error":    err.Error(),
		})
		failed, updateErr := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", err.Error())
		if updateErr == nil {
			events <- CollaborationEvent{Type: "collaboration_step", Step: failed}
		}
		return "", err
	}
	output := completion.Text
	r.trace.LLMEnd(ctx, span, map[string]any{
		"role":                  role,
		"agent_id":              agentID,
		"model":                 completion.Model,
		"output":                output,
		"output_chars":          len(output),
		"prompt_tokens":         completion.Usage.PromptTokens,
		"completion_tokens":     completion.Usage.CompletionTokens,
		"total_tokens":          completion.Usage.TotalTokens,
		"token_usage_estimated": completion.Usage.Estimated,
	})
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		return "", err
	}
	events <- CollaborationEvent{Type: "collaboration_step", Step: completed}
	return output, nil
}

func plannerPrompt() string {
	return "You are the Planner collaboration role. Break the user task into 2-5 concrete steps, name key assumptions, and define success criteria. Do not execute the task."
}

func workerPrompt(agent domain.Agent) string {
	return fmt.Sprintf("You are the Worker collaboration role. Execute the plan using this selected agent persona.\n\nWorker agent: %s\nDescription: %s\nPersona instructions: %s\n\nProduce the best task result you can. Do not review your own work.",
		agent.Name,
		agent.Description,
		agent.SystemPrompt,
	)
}

func reviewerPrompt() string {
	return "You are the Reviewer collaboration role. Check the worker result against the user task and plan. Identify quality issues, risks, missing details, and whether the result is ready. Do not rewrite the final answer."
}

func finalizerPrompt() string {
	return "You are the Finalizer collaboration role. Produce the final user-facing answer by combining the plan, worker result, and reviewer notes. Be concise and do not mention internal implementation details unless relevant."
}

func emitFinalDeltas(ctx context.Context, text string, events chan<- CollaborationEvent) {
	parts := strings.SplitAfter(strings.TrimSpace(text), " ")
	for _, part := range parts {
		if part == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case events <- CollaborationEvent{Type: "delta", Delta: part}:
			time.Sleep(15 * time.Millisecond)
		}
	}
}

func NormalizeChatMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "", ChatModeSingle:
		return ChatModeSingle
	case ChatModeMultiAgent:
		return ChatModeMultiAgent
	case ChatModeAutonomous:
		return ChatModeAutonomous
	default:
		return ChatModeSingle
	}
}

func NormalizeRouterMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case RouterModeQuery:
		return RouterModeQuery
	default:
		return RouterModeAuto
	}
}

func IsAgentNotFound(err error) bool {
	return store.IsNotFound(err)
}
