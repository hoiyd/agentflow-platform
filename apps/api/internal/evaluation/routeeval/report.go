package routeeval

import (
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

type Config struct {
	Policy           agent.RoutingEvaluationPolicy `json:"policy"`
	RouterMode       string                        `json:"router_mode"`
	PromptRevision   string                        `json:"prompt_revision"`
	PromptHash       string                        `json:"prompt_hash"`
	AgentCatalogHash string                        `json:"agent_catalog_hash"`
	Model            string                        `json:"model,omitempty"`
	Provider         string                        `json:"provider,omitempty"`
	Trials           int                           `json:"trials"`
	MaxModelCalls    int                           `json:"max_model_calls,omitempty"`
	MaxTotalTokens   int                           `json:"max_total_tokens,omitempty"`
	SampleTimeoutMS  int64                         `json:"sample_timeout_ms,omitempty"`
}

type Sample struct {
	CaseID              string                             `json:"case_id"`
	Split               string                             `json:"split"`
	Trial               int                                `json:"trial"`
	Coverage            []string                           `json:"coverage"`
	Status              string                             `json:"status"`
	ExpectedOutcome     string                             `json:"expected_outcome"`
	AcceptableAgentIDs  []string                           `json:"acceptable_agent_ids,omitempty"`
	ActualOutcome       string                             `json:"actual_outcome,omitempty"`
	DecisionMode        string                             `json:"decision_mode,omitempty"`
	SelectedAgentID     string                             `json:"selected_agent_id,omitempty"`
	ProposedAgentID     string                             `json:"proposed_agent_id,omitempty"`
	EligibleAgentIDs    []string                           `json:"eligible_agent_ids"`
	AcceptableSelection bool                               `json:"acceptable_selection"`
	EligibleRecall      bool                               `json:"eligible_recall"`
	UnsafeFalseRoute    bool                               `json:"unsafe_false_route"`
	InvalidResponse     bool                               `json:"invalid_response"`
	FallbackAttempted   bool                               `json:"fallback_attempted"`
	FallbackRecovered   bool                               `json:"fallback_recovered"`
	FailureCode         string                             `json:"failure_code,omitempty"`
	Reason              string                             `json:"reason,omitempty"`
	TopScore            int                                `json:"top_score"`
	RunnerUpScore       int                                `json:"runner_up_score"`
	ScoreMargin         int                                `json:"score_margin"`
	Confidence          float64                            `json:"confidence"`
	RequirementCoverage float64                            `json:"requirement_coverage"`
	Candidates          []agent.RoutingEvaluationCandidate `json:"candidates"`
	ModelCalls          int                                `json:"model_calls"`
	InputTokens         int                                `json:"input_tokens"`
	OutputTokens        int                                `json:"output_tokens"`
	RouterTokens        int                                `json:"router_tokens"`
	UsageEstimated      bool                               `json:"usage_estimated"`
	LatencyMS           int64                              `json:"latency_ms"`
	ActualModel         string                             `json:"actual_model,omitempty"`
}

type Metric struct {
	Value       *float64 `json:"value"`
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
}

type Summary struct {
	Samples                 int     `json:"samples"`
	Evaluated               int     `json:"evaluated"`
	Failed                  int     `json:"failed"`
	NotEvaluated            int     `json:"not_evaluated"`
	EligibleRecall          Metric  `json:"eligible_recall"`
	Top1AcceptableSelection Metric  `json:"top_1_acceptable_selection"`
	UnsafeFalseRoute        Metric  `json:"unsafe_false_route"`
	NoRoutePrecision        Metric  `json:"no_route_precision"`
	NoRouteRecall           Metric  `json:"no_route_recall"`
	InvalidResponse         Metric  `json:"invalid_response"`
	FallbackRecovery        Metric  `json:"fallback_recovery"`
	MeanInputTokens         float64 `json:"mean_input_tokens"`
	MeanOutputTokens        float64 `json:"mean_output_tokens"`
	MeanRouterTokens        float64 `json:"mean_router_tokens"`
	MeanLatencyMS           float64 `json:"mean_latency_ms"`
}

type Thresholds struct {
	MinimumScore         int     `json:"minimum_score"`
	MinimumScoreMargin   int     `json:"minimum_score_margin"`
	MinimumLLMConfidence float64 `json:"minimum_llm_confidence"`
}

type Calibration struct {
	Split          string     `json:"split"`
	Method         string     `json:"method"`
	Candidates     int        `json:"candidates"`
	Recommendation Thresholds `json:"recommendation"`
	Calibration    Summary    `json:"calibration_summary"`
	Holdout        Summary    `json:"holdout_summary"`
}

type Report struct {
	evalreport.Identity
	SchemaVersion string             `json:"schema_version"`
	Config        Config             `json:"config"`
	CostSource    string             `json:"cost_source"`
	Samples       []Sample           `json:"samples"`
	Summary       map[string]Summary `json:"summary"`
	Calibration   *Calibration       `json:"calibration,omitempty"`
	Gate          evalreport.Gate    `json:"gate"`
}
