package agent

import (
	"fmt"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

// selectWorkerAgentV1 preserves the original central-keyword fallback for
// replay comparison only; v13 snapshots are no longer resumable.
func selectWorkerAgentV1(agents []domain.Agent, task string, plan string) routeDecision {
	query := strings.ToLower(task + "\n" + plan)
	scores := make([]agentScore, 0, len(agents))
	for _, agent := range agents {
		score, reason := scoreAgentForTaskV1(agent, query)
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
		Agent: selected.Agent, Mode: RouterModeQuery, Reason: selected.Reason,
		Score: selected.Score, Scores: scores,
	}
	return decision
}

func scoreAgentForTaskV1(agent domain.Agent, query string) (int, string) {
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
			score++
			profileMatches = append(profileMatches, token)
		}
	}
	if len(profileMatches) > 0 {
		reasons = append(reasons, fmt.Sprintf("profile term overlap +%d: %s", len(profileMatches), strings.Join(profileMatches, ", ")))
	}

	for _, rule := range routingRulesV1() {
		if !strings.Contains(profile, rule.ProfileHint) {
			continue
		}
		matches := matchedKeywordsV1(query, rule.Keywords)
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

type routingRuleV1 struct {
	Label       string
	ProfileHint string
	Keywords    []string
	Weight      int
}

func routingRulesV1() []routingRuleV1 {
	return []routingRuleV1{
		{Label: "software implementation/debugging", ProfileHint: "software", Keywords: []string{"code", "coding", "implement", "implementation", "bug", "debug", "frontend", "backend", "api", "test", "typescript", "go", "react", "css", "代码", "实现", "修复", "前端", "后端", "测试", "接口"}, Weight: 5},
		{Label: "research and external context", ProfileHint: "research", Keywords: []string{"research", "market", "compare", "source", "sources", "verify", "news", "place", "product", "pricing", "competitor", "调研", "市场", "比较", "来源", "验证", "新闻", "产品", "价格", "竞品"}, Weight: 5},
		{Label: "operations and quantitative analysis", ProfileHint: "operational", Keywords: []string{"budget", "cost", "capacity", "schedule", "calculate", "calculation", "metric", "forecast", "tradeoff", "operations", "data", "预算", "成本", "容量", "排期", "计算", "指标", "预测", "取舍", "数据"}, Weight: 5},
		{Label: "strategy, narrative, and planning", ProfileHint: "storyline", Keywords: []string{"plan", "brief", "story", "storyline", "launch", "audience", "message", "strategy", "roadmap", "proposal", "decision", "计划", "简报", "故事", "发布", "受众", "策略", "路线图", "方案", "决策"}, Weight: 5},
	}
}

func matchedKeywordsV1(query string, keywords []string) []string {
	matches := []string{}
	for _, keyword := range keywords {
		if strings.Contains(query, keyword) {
			matches = append(matches, keyword)
		}
	}
	return matches
}
