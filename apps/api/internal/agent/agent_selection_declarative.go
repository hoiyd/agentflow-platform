package agent

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	// These weights define the declarative ranker used by the current v3 policy
	// and the evaluation-only v2 migration baseline.
	capabilityHintWeight = 6
	taskExampleWeight    = 3
	toolHintWeight       = 4
	preferenceHintWeight = 2
	profileTermWeight    = 1
	exclusionHintWeight  = 8
)

func selectWorkerAgentV2Baseline(agents []domain.Agent, task string, plan string, requirements domain.AgentRoutingRequirements) (routeDecision, error) {
	decision := rankWorkerAgentsDeclarative(agents, task, plan, requirements)
	if decision.Agent.ID == "" || decision.Score <= 0 {
		decision.Agent = domain.Agent{}
		decision.Reason = "No candidate had positive declarative routing evidence."
		return decision, ErrNoSuitableAgent
	}
	return decision, nil
}

func rankWorkerAgentsDeclarative(agents []domain.Agent, task string, plan string, requirements domain.AgentRoutingRequirements) routeDecision {
	query := normalizeRoutingText(task + "\n" + plan)
	queryTokens := routingTokens(query, 2)
	scores := make([]agentScore, 0, len(agents))
	for _, agent := range agents {
		score, reason := scoreAgentForTaskDeclarative(agent, query, queryTokens, requirements.PreferredCapabilities)
		scores = append(scores, agentScore{Agent: agent, Score: score, Reason: reason})
	}
	sort.SliceStable(scores, func(i, j int) bool {
		if scores[i].Score == scores[j].Score {
			if scores[i].Agent.Name == scores[j].Agent.Name {
				return scores[i].Agent.ID < scores[j].Agent.ID
			}
			return scores[i].Agent.Name < scores[j].Agent.Name
		}
		return scores[i].Score > scores[j].Score
	})
	if len(scores) == 0 || scores[0].Score <= 0 {
		decision := routeDecision{
			Mode: RouterModeQuery, Reason: "No candidate had positive declarative routing evidence.",
			Scores: scores,
		}
		if len(scores) > 0 {
			decision.Agent = scores[0].Agent
			decision.Score = scores[0].Score
		}
		return decision
	}
	selected := scores[0]
	decision := routeDecision{
		Agent: selected.Agent, Mode: RouterModeQuery, Reason: selected.Reason,
		Score: selected.Score, Scores: scores,
	}
	return decision
}

func scoreAgentForTaskDeclarative(agent domain.Agent, query string, queryTokens map[string]bool, preferredCapabilities []string) (int, string) {
	score := 0
	reasons := make([]string, 0, 5)

	capabilities := matchedRoutingHints(query, queryTokens, agent.RoutingHints.Capabilities, 1)
	if len(capabilities) > 0 {
		delta := capabilityHintWeight * len(capabilities)
		score += delta
		reasons = append(reasons, fmt.Sprintf("capability hints +%d: %s", delta, summarizeHints(capabilities)))
	}
	preferredQuery := normalizeRoutingText(strings.Join(preferredCapabilities, " "))
	preferenceMatches := matchedRoutingHints(preferredQuery, routingTokens(preferredQuery, 2), agent.RoutingHints.Capabilities, 1)
	if len(preferenceMatches) > 0 {
		delta := preferenceHintWeight * len(preferenceMatches)
		score += delta
		reasons = append(reasons, fmt.Sprintf("preferred capabilities +%d: %s", delta, summarizeHints(preferenceMatches)))
	}

	exampleMatches := 0
	for _, example := range agent.RoutingHints.TaskExamples {
		exampleMatches += routingTokenOverlap(queryTokens, example, 3)
	}
	if exampleMatches > 0 {
		delta := taskExampleWeight * exampleMatches
		score += delta
		reasons = append(reasons, fmt.Sprintf("task examples +%d", delta))
	}

	toolMatches := matchedRoutingHints(query, queryTokens, agent.Tools, 1)
	if len(toolMatches) > 0 {
		delta := toolHintWeight * len(toolMatches)
		score += delta
		reasons = append(reasons, fmt.Sprintf("tool hints +%d: %s", delta, summarizeHints(toolMatches)))
	}

	profileMatches := routingTokenOverlap(queryTokens, agent.Name+" "+agent.Description, 4)
	if profileMatches > 0 {
		delta := profileTermWeight * profileMatches
		score += delta
		reasons = append(reasons, fmt.Sprintf("profile terms +%d", delta))
	}

	exclusions := matchedRoutingHints(query, queryTokens, agent.RoutingHints.Exclusions, 2)
	if len(exclusions) > 0 {
		delta := exclusionHintWeight * len(exclusions)
		score -= delta
		reasons = append(reasons, fmt.Sprintf("excluded task hints -%d: %s", delta, summarizeHints(exclusions)))
	}

	if score < 0 {
		score = 0
	}
	if len(reasons) == 0 {
		return 0, "no declarative routing evidence"
	}
	return score, strings.Join(reasons, "; ")
}

func matchedRoutingHints(query string, queryTokens map[string]bool, hints []string, minimumOverlap int) []string {
	matches := make([]string, 0, len(hints))
	for _, hint := range hints {
		normalized := normalizeRoutingText(hint)
		if normalized == "" {
			continue
		}
		if routingHintMatches(query, queryTokens, normalized, minimumOverlap) {
			matches = append(matches, hint)
		}
	}
	return matches
}

func routingHintMatches(query string, queryTokens map[string]bool, hint string, minimumOverlap int) bool {
	if containsNonASCII(hint) && strings.Contains(query, hint) {
		return true
	}
	hintTokens := routingTokens(hint, 2)
	if len(hintTokens) == 1 {
		for token := range hintTokens {
			return queryTokens[token]
		}
	}
	return strings.Contains(query, hint) || routingTokenOverlap(queryTokens, hint, 2) >= minimumOverlap
}

func containsNonASCII(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}

func routingTokenOverlap(queryTokens map[string]bool, text string, minimumLength int) int {
	count := 0
	for token := range routingTokens(normalizeRoutingText(text), minimumLength) {
		if queryTokens[token] {
			count++
		}
	}
	return count
}

func routingTokens(text string, minimumLength int) map[string]bool {
	result := map[string]bool{}
	for _, token := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if utf8.RuneCountInString(token) >= minimumLength {
			result[token] = true
		}
	}
	return result
}

func normalizeRoutingText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func summarizeHints(hints []string) string {
	if len(hints) > 3 {
		hints = hints[:3]
	}
	return strings.Join(hints, ", ")
}
