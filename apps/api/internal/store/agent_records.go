package store

import (
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func DefaultAgents(now time.Time) []domain.Agent {
	agents := []domain.Agent{
		{
			ID:           "agent_research",
			Name:         "Field Researcher",
			Description:  "Analyzes supplied research material, separates supported facts from open questions, and identifies evidence gaps.",
			SystemPrompt: "You are AgentFlow's Field Researcher. Analyze the supplied context, distinguish supported facts from assumptions, compare evidence carefully, and call out uncertainty instead of filling gaps with guesses.",
			RoutingHints: domain.AgentRoutingHints{
				Capabilities: []string{"research", "sources", "verification", "market", "pricing", "competitors", "调研", "来源", "验证", "市场", "价格", "竞品"},
				TaskExamples: []string{"Compare competitors and verify recent pricing sources.", "Research a product or market and identify evidence gaps."},
				Exclusions:   []string{"implement or debug software", "calculate a budget or capacity forecast"},
			},
			Tools:     nil,
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:           "agent_coding",
			Name:         "Systems Builder",
			Description:  "Turns implementation requests into concrete technical steps, debugging hypotheses, and maintainable code changes.",
			SystemPrompt: "You are AgentFlow's Systems Builder. Focus on software behavior, interfaces, edge cases, and implementation tradeoffs. Give direct engineering guidance, identify risks, and prefer concrete next steps over broad advice.",
			RoutingHints: domain.AgentRoutingHints{
				Capabilities: []string{"software", "code", "implementation", "debugging", "frontend", "backend", "api", "testing", "go", "typescript", "react", "css", "代码", "实现", "修复", "前端", "后端", "接口", "测试"},
				TaskExamples: []string{"Implement and test a Go API change.", "Debug a frontend component and fix the underlying code."},
				Exclusions:   []string{"market research and source comparison", "budget forecasting"},
			},
			Tools:     []string{"calculator", "get_current_time"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:           "agent_data",
			Name:         "Operations Analyst",
			Description:  "Evaluates budgets, schedules, capacity, and tradeoffs with explicit assumptions and calculation-backed reasoning.",
			SystemPrompt: "You are AgentFlow's Operations Analyst. Treat questions as operational decisions involving cost, time, capacity, or prioritization. Show assumptions, calculate carefully, compare scenarios, and make the practical tradeoff visible.",
			RoutingHints: domain.AgentRoutingHints{
				Capabilities: []string{"budget", "cost", "capacity", "schedule", "calculation", "metrics", "forecast", "tradeoff", "operations", "data", "预算", "成本", "容量", "排期", "计算", "指标", "预测", "取舍", "数据"},
				TaskExamples: []string{"Calculate a quarterly budget and compare cost scenarios.", "Forecast capacity and explain operational tradeoffs."},
				Exclusions:   []string{"write software", "research external sources"},
			},
			Tools:     []string{"calculator"},
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:           "agent_planner",
			Name:         "Narrative Strategist",
			Description:  "Shapes messy goals into audience-aware briefs, storylines, launch plans, and decision-ready next actions.",
			SystemPrompt: "You are AgentFlow's Narrative Strategist. Clarify the audience, intent, and constraints behind a request. Convert goals into concise briefs, storylines, communication plans, or ordered next actions with dependencies and risks.",
			RoutingHints: domain.AgentRoutingHints{
				Capabilities: []string{"plan", "brief", "story", "storyline", "launch", "audience", "message", "strategy", "roadmap", "proposal", "decision", "计划", "简报", "故事", "发布", "受众", "策略", "路线图", "方案", "决策"},
				TaskExamples: []string{"Turn an ambiguous goal into an ordered roadmap.", "Create an audience-aware launch brief and communication plan."},
				Exclusions:   []string{"debug software", "calculate detailed financial metrics"},
			},
			Tools:     []string{"get_current_time"},
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	for index := range agents {
		agents[index] = domain.NormalizeAgentConfig(agents[index])
	}
	return agents
}

func updateDefaultAgentText(agent *domain.Agent, next domain.Agent) bool {
	changed := false
	old := oldDefaultAgentText(agent.ID)
	if agent.Name == "" || agent.Name == old.Name {
		agent.Name = next.Name
		changed = true
	}
	if agent.Description == "" || agent.Description == old.Description {
		agent.Description = next.Description
		changed = true
	}
	if agent.SystemPrompt == "" || agent.SystemPrompt == old.SystemPrompt {
		agent.SystemPrompt = next.SystemPrompt
		changed = true
	}
	if domain.IsDefaultAgentID(agent.ID) &&
		len(agent.RoutingHints.Capabilities) == 0 &&
		len(agent.RoutingHints.TaskExamples) == 0 &&
		len(agent.RoutingHints.Exclusions) == 0 {
		agent.RoutingHints = next.RoutingHints
		changed = true
	}
	return changed
}

func oldDefaultAgentText(id string) domain.Agent {
	switch id {
	case "agent_research":
		return domain.Agent{
			Name:         "Research Agent",
			Description:  "Finds, compares, and summarizes information using available search and remote data tools.",
			SystemPrompt: "You are AgentFlow's Research Agent. Be precise, cite tool-derived facts when available, compare options carefully, and say when evidence is missing.",
		}
	case "agent_coding":
		return domain.Agent{
			Name:         "Coding Assistant Agent",
			Description:  "Helps reason about implementation details, debugging steps, and code changes.",
			SystemPrompt: "You are AgentFlow's Coding Assistant Agent. Give direct engineering guidance, identify risks, and prefer concrete implementation steps.",
		}
	case "agent_data":
		return domain.Agent{
			Name:         "Data Analyst Agent",
			Description:  "Analyzes structured information, calculations, and data-oriented questions.",
			SystemPrompt: "You are AgentFlow's Data Analyst Agent. Work carefully with numbers, show assumptions, and use tools for calculations when useful.",
		}
	case "agent_planner":
		return domain.Agent{
			Name:         "Planner Agent",
			Description:  "Breaks ambiguous requests into ordered plans and tracks next actions.",
			SystemPrompt: "You are AgentFlow's Planner Agent. Convert goals into clear, ordered plans with dependencies, risks, and next actions.",
		}
	default:
		return domain.Agent{}
	}
}

func NormalizeTools(items []string) []string {
	seen := map[string]bool{}
	tools := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			tools = append(tools, item)
		}
	}
	sort.Strings(tools)
	return tools
}
