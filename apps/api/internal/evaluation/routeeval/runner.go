// Package routeeval benchmarks the production Agent selection policy against a
// frozen calibration/holdout dataset. It never creates Runs or child Runs.
package routeeval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tools"
)

const (
	SchemaVersion        = "agent-routing-eval-v1"
	DatasetSchemaVersion = "agent-routing-dataset-v1"
	PromptRevision       = agent.AgentRoutingPromptRevision
	SplitCalibration     = "calibration"
	SplitHoldout         = "holdout"
)

type Options struct {
	DatasetPath    string        `json:"dataset_path"`
	RouterMode     string        `json:"router_mode"`
	Trials         int           `json:"trials"`
	MaxModelCalls  int           `json:"max_model_calls,omitempty"`
	MaxTotalTokens int           `json:"max_total_tokens,omitempty"`
	Timeout        time.Duration `json:"sample_timeout_ns,omitempty"`
	Revision       string        `json:"git_revision"`
	Model          string        `json:"model,omitempty"`
	Provider       string        `json:"provider,omitempty"`
}

type Dataset struct {
	SchemaVersion string           `json:"schema_version"`
	ID            string           `json:"id"`
	Version       string           `json:"version"`
	Agents        []domain.Agent   `json:"agents"`
	Cases         []EvaluationCase `json:"cases"`
}

type EvaluationCase struct {
	ID                 string                          `json:"id"`
	Split              string                          `json:"split"`
	Coverage           []string                        `json:"coverage"`
	Task               string                          `json:"task"`
	Plan               string                          `json:"plan"`
	Requirements       domain.AgentRoutingRequirements `json:"requirements,omitempty"`
	ExpectedOutcome    string                          `json:"expected_outcome"`
	AcceptableAgentIDs []string                        `json:"acceptable_agent_ids,omitempty"`
}

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

func Run(ctx context.Context, completer modelprovider.TextCompleter, opts Options) (Report, error) {
	data, datasetHash, err := loadDataset(opts.DatasetPath)
	if err != nil {
		return Report{}, err
	}
	policy := agent.AgentSelectionPolicyEvidence()
	if opts.RouterMode == "" {
		opts.RouterMode = agent.RouterModeQuery
	}
	if opts.RouterMode != agent.RouterModeQuery && opts.RouterMode != agent.RouterModeAuto {
		return Report{}, fmt.Errorf("router mode must be %q or %q", agent.RouterModeQuery, agent.RouterModeAuto)
	}
	if opts.RouterMode == agent.RouterModeQuery {
		opts.Trials = 1
	} else if completer == nil || opts.Trials < 1 || opts.Trials > 20 || opts.MaxModelCalls < 1 || opts.MaxTotalTokens < 1 || opts.Timeout <= 0 || opts.Timeout > 5*time.Minute {
		return Report{}, errors.New("live routing requires a model client, 1-20 trials, positive call/token budgets and sample timeout <= 5m")
	}
	catalog := tools.DefaultCatalog()
	systemPrompt, _ := agent.AgentRoutingPrompts("", "", domain.AgentRoutingRequirements{}, nil)
	startedAt := time.Now().UTC()
	report := Report{
		Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "agent_routing", DatasetID: data.ID,
			DatasetVersion: data.Version, DatasetHash: datasetHash, GitRevision: normalizeRevision(opts.Revision), StartedAt: startedAt},
		SchemaVersion: SchemaVersion,
		Config: Config{Policy: policy, RouterMode: opts.RouterMode, PromptRevision: PromptRevision,
			PromptHash: digest([]byte(systemPrompt)), AgentCatalogHash: agentCatalogHash(data.Agents), Model: opts.Model,
			Provider: opts.Provider, Trials: opts.Trials, MaxModelCalls: opts.MaxModelCalls,
			MaxTotalTokens: opts.MaxTotalTokens, SampleTimeoutMS: opts.Timeout.Milliseconds()},
		CostSource: "not_applicable: deterministic routing with no model request",
		Samples:    []Sample{}, Summary: map[string]Summary{},
	}
	if opts.RouterMode == agent.RouterModeAuto {
		report.CostSource = "unavailable: no price table; router token usage only"
	}
	remainingCalls, remainingTokens := opts.MaxModelCalls, opts.MaxTotalTokens
	stop := ""
	for trial := 1; trial <= opts.Trials; trial++ {
		for _, item := range data.Cases {
			sample := Sample{CaseID: item.ID, Split: item.Split, Trial: trial, Coverage: append([]string(nil), item.Coverage...),
				Status: "completed", ExpectedOutcome: item.ExpectedOutcome, AcceptableAgentIDs: append([]string(nil), item.AcceptableAgentIDs...),
				EligibleAgentIDs: []string{}, Candidates: []agent.RoutingEvaluationCandidate{}}
			if ctx.Err() != nil {
				stop = "canceled"
			}
			if opts.RouterMode == agent.RouterModeAuto && (remainingCalls <= 0 || remainingTokens <= 0) {
				stop = "suite_budget_exhausted"
			}
			if stop != "" {
				sample.Status, sample.FailureCode, sample.Reason = "not_evaluated", stop, stop
			} else {
				result, usage, latency, actualModel := evaluateSample(ctx, completer, catalog, data.Agents, item, opts, remainingTokens)
				applyResult(&sample, result, usage, latency, actualModel)
				remainingCalls -= sample.ModelCalls
				remainingTokens -= sample.RouterTokens
			}
			report.Samples = append(report.Samples, sample)
		}
	}
	report.Summary = summarizeBySplit(report.Samples)
	report.Calibration = calibrate(report.Samples)
	report.Gate = gate(report.Samples)
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func evaluateSample(ctx context.Context, completer modelprovider.TextCompleter, catalog *tools.Catalog, agents []domain.Agent, item EvaluationCase, opts Options, remainingTokens int) (agent.RoutingEvaluationResult, modelprovider.Usage, int64, string) {
	input := agent.RoutingEvaluationInput{Agents: agents, Catalog: catalog, Task: item.Task, Plan: item.Plan,
		Requirements: item.Requirements, RouterMode: opts.RouterMode}
	started := time.Now()
	if opts.RouterMode == agent.RouterModeAuto {
		eligible := eligibleAgents(agents, catalog, item.Requirements)
		if len(eligible) == 0 {
			return agent.EvaluateAgentRouting(input), modelprovider.Usage{}, time.Since(started).Milliseconds(), ""
		}
		systemPrompt, userPrompt := agent.AgentRoutingPrompts(item.Task, item.Plan, item.Requirements, eligible)
		estimatedPromptTokens := estimateTokens(systemPrompt + "\n" + userPrompt)
		if remainingTokens <= estimatedPromptTokens {
			return agent.RoutingEvaluationResult{Outcome: agent.AgentSelectionOutcomeRouterFailed, FailureCode: "suite_token_budget_exhausted", Reason: "suite token budget exhausted before model request"}, modelprovider.Usage{}, time.Since(started).Milliseconds(), ""
		}
		callCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
		completion, err := completer.CompleteTextDetailed(callCtx, systemPrompt, userPrompt)
		cancel()
		input.ModelResponse, input.ModelError = completion.Text, err
		usage := completion.Usage
		if !usage.Valid() {
			usage = modelprovider.Usage{PromptTokens: estimatedPromptTokens, CompletionTokens: estimateTokens(completion.Text), Estimated: true}
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
		return agent.EvaluateAgentRouting(input), usage, time.Since(started).Milliseconds(), completion.Model
	}
	return agent.EvaluateAgentRouting(input), modelprovider.Usage{}, time.Since(started).Milliseconds(), ""
}

func applyResult(sample *Sample, result agent.RoutingEvaluationResult, usage modelprovider.Usage, latency int64, actualModel string) {
	sample.ActualOutcome, sample.DecisionMode, sample.SelectedAgentID, sample.ProposedAgentID = result.Outcome, result.Mode, result.SelectedAgentID, result.ProposedAgentID
	sample.InvalidResponse, sample.FailureCode, sample.Reason = result.InvalidResponse, result.FailureCode, result.Reason
	sample.TopScore, sample.RunnerUpScore, sample.ScoreMargin = result.TopScore, result.RunnerUpScore, result.ScoreMargin
	sample.Confidence, sample.RequirementCoverage, sample.Candidates = result.Confidence, result.RequirementCoverage, result.Candidates
	sample.FallbackAttempted = result.FallbackReasonCode != ""
	sample.FallbackRecovered = sample.FallbackAttempted && result.Outcome != agent.AgentSelectionOutcomeRouterFailed
	for _, candidate := range result.Candidates {
		if candidate.Eligible {
			sample.EligibleAgentIDs = append(sample.EligibleAgentIDs, candidate.AgentID)
		}
	}
	sample.EligibleRecall = intersects(sample.EligibleAgentIDs, sample.AcceptableAgentIDs)
	sample.AcceptableSelection = contains(sample.AcceptableAgentIDs, sample.SelectedAgentID)
	sample.UnsafeFalseRoute = result.Outcome == agent.AgentSelectionOutcomeSelected && !sample.AcceptableSelection
	sample.ModelCalls, sample.RouterTokens, sample.UsageEstimated, sample.LatencyMS = 0, usage.TotalTokens, usage.Estimated, latency
	sample.ActualModel = actualModel
	if usage.Valid() {
		sample.ModelCalls = 1
	}
	if result.Outcome == agent.AgentSelectionOutcomeRouterFailed {
		sample.Status = "failed"
	}
	if result.FailureCode == "suite_token_budget_exhausted" {
		sample.Status = "not_evaluated"
	}
	if sample.Reason != "" {
		sample.Reason, _ = redaction.Text(sample.Reason)
	}
}

func summarizeBySplit(samples []Sample) map[string]Summary {
	return map[string]Summary{
		"overall":        summarize(samples, ""),
		SplitCalibration: summarize(samples, SplitCalibration),
		SplitHoldout:     summarize(samples, SplitHoldout),
	}
}

func summarize(samples []Sample, split string) Summary {
	var result Summary
	var eligibleOK, eligibleTotal, selectedOK, selectedTotal int
	var unsafe, predictedNoRoute, expectedNoRoute, correctNoRoute, invalid, modelCalls, fallbackAttempts, fallbackRecovered int
	var tokenTotal, latencyTotal int64
	for _, sample := range samples {
		if split != "" && sample.Split != split {
			continue
		}
		result.Samples++
		switch sample.Status {
		case "not_evaluated":
			result.NotEvaluated++
		case "failed":
			result.Evaluated++
			result.Failed++
		default:
			result.Evaluated++
		}
		if sample.ExpectedOutcome == agent.AgentSelectionOutcomeSelected {
			eligibleTotal++
			selectedTotal++
			if sample.EligibleRecall {
				eligibleOK++
			}
			if sample.AcceptableSelection {
				selectedOK++
			}
		}
		if sample.UnsafeFalseRoute {
			unsafe++
		}
		predicted := isNoRoute(sample.ActualOutcome)
		expected := isNoRoute(sample.ExpectedOutcome)
		if predicted {
			predictedNoRoute++
		}
		if expected {
			expectedNoRoute++
		}
		if predicted && expected {
			correctNoRoute++
		}
		if sample.InvalidResponse {
			invalid++
		}
		modelCalls += sample.ModelCalls
		if sample.FallbackAttempted {
			fallbackAttempts++
		}
		if sample.FallbackRecovered {
			fallbackRecovered++
		}
		tokenTotal += int64(sample.RouterTokens)
		latencyTotal += sample.LatencyMS
	}
	result.EligibleRecall = metric(eligibleOK, eligibleTotal)
	result.Top1AcceptableSelection = metric(selectedOK, selectedTotal)
	result.UnsafeFalseRoute = metric(unsafe, result.Samples)
	result.NoRoutePrecision = metric(correctNoRoute, predictedNoRoute)
	result.NoRouteRecall = metric(correctNoRoute, expectedNoRoute)
	result.InvalidResponse = metric(invalid, modelCalls)
	result.FallbackRecovery = metric(fallbackRecovered, fallbackAttempts)
	if result.Evaluated > 0 {
		result.MeanRouterTokens = float64(tokenTotal) / float64(result.Evaluated)
		result.MeanLatencyMS = float64(latencyTotal) / float64(result.Evaluated)
	}
	return result
}

func calibrate(samples []Sample) *Calibration {
	calibrationSamples := filterSamples(samples, SplitCalibration)
	holdoutSamples := filterSamples(samples, SplitHoldout)
	scores, margins, confidences := []int{0}, []int{0}, []float64{0}
	for _, sample := range calibrationSamples {
		scores = append(scores, sample.TopScore, sample.TopScore+1)
		margins = append(margins, sample.ScoreMargin, sample.ScoreMargin+1)
		if sample.DecisionMode == "llm" {
			confidences = append(confidences, sample.Confidence, math.Round(min(1, sample.Confidence+0.01)*100)/100)
		}
	}
	scores, margins, confidences = uniqueSorted(scores), uniqueSorted(margins), uniqueSortedFloats(confidences)
	best := Thresholds{}
	bestCorrect, bestUnsafe, candidateCount := -1, 0, 0
	for _, score := range scores {
		for _, margin := range margins {
			for _, confidence := range confidences {
				candidateCount++
				thresholds := Thresholds{MinimumScore: score, MinimumScoreMargin: margin, MinimumLLMConfidence: confidence}
				correct, unsafe := thresholdQuality(calibrationSamples, thresholds)
				strictness := float64(score+margin) + confidence
				bestStrictness := float64(best.MinimumScore+best.MinimumScoreMargin) + best.MinimumLLMConfidence
				if correct > bestCorrect || (correct == bestCorrect && unsafe < bestUnsafe) ||
					(correct == bestCorrect && unsafe == bestUnsafe && strictness < bestStrictness) {
					best, bestCorrect, bestUnsafe = thresholds, correct, unsafe
				}
			}
		}
	}
	return &Calibration{Split: SplitCalibration, Method: "maximize exact outcomes, then minimize unsafe false routes and threshold strictness",
		Candidates: candidateCount, Recommendation: best,
		Calibration: summarize(applyThresholds(calibrationSamples, best), ""), Holdout: summarize(applyThresholds(holdoutSamples, best), "")}
}

func applyThresholds(samples []Sample, thresholds Thresholds) []Sample {
	result := append([]Sample(nil), samples...)
	for index := range result {
		sample := &result[index]
		if sample.ActualOutcome == agent.AgentSelectionOutcomeNoEligible || sample.Status != "completed" {
			continue
		}
		passes := sample.TopScore >= thresholds.MinimumScore &&
			(len(sample.EligibleAgentIDs) < 2 || sample.ScoreMargin >= thresholds.MinimumScoreMargin) &&
			(sample.DecisionMode != "llm" || sample.Confidence >= thresholds.MinimumLLMConfidence)
		sample.SelectedAgentID = sample.ProposedAgentID
		if passes {
			sample.ActualOutcome = agent.AgentSelectionOutcomeSelected
		} else {
			sample.ActualOutcome, sample.SelectedAgentID = agent.AgentSelectionOutcomeNoSuitable, ""
		}
		sample.AcceptableSelection = contains(sample.AcceptableAgentIDs, sample.SelectedAgentID)
		sample.UnsafeFalseRoute = sample.ActualOutcome == agent.AgentSelectionOutcomeSelected && !sample.AcceptableSelection
	}
	return result
}

func thresholdQuality(samples []Sample, thresholds Thresholds) (int, int) {
	correct, unsafe := 0, 0
	for _, sample := range applyThresholds(samples, thresholds) {
		if sample.ExpectedOutcome == sample.ActualOutcome && (sample.ExpectedOutcome != agent.AgentSelectionOutcomeSelected || sample.AcceptableSelection) {
			correct++
		}
		if sample.UnsafeFalseRoute {
			unsafe++
		}
	}
	return correct, unsafe
}

func gate(samples []Sample) evalreport.Gate {
	holdout := filterSamples(samples, SplitHoldout)
	failures := 0
	for _, sample := range holdout {
		passed := sample.Status == "completed" && sample.ExpectedOutcome == sample.ActualOutcome
		if sample.ExpectedOutcome == agent.AgentSelectionOutcomeSelected {
			passed = passed && sample.AcceptableSelection
		}
		if !passed || sample.UnsafeFalseRoute {
			failures++
		}
	}
	result := evalreport.Gate{Passed: len(holdout) > 0 && failures == 0, BlockingSamples: len(holdout), BlockingFailures: failures, Reasons: []string{}}
	if !result.Passed {
		result.Reasons = []string{"one or more holdout routing outcomes were incorrect, failed, or not evaluated"}
	}
	return result
}

func loadDataset(path string) (Dataset, string, error) {
	if strings.TrimSpace(path) == "" {
		return Dataset{}, "", errors.New("routing evaluation dataset path is required")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, "", fmt.Errorf("read routing dataset: %w", err)
	}
	var data Dataset
	if err := json.Unmarshal(content, &data); err != nil {
		return Dataset{}, "", fmt.Errorf("parse routing dataset: %w", err)
	}
	if err := validateDataset(&data); err != nil {
		return Dataset{}, "", err
	}
	return data, digest(content), nil
}

func validateDataset(data *Dataset) error {
	if data.SchemaVersion != DatasetSchemaVersion || strings.TrimSpace(data.ID) == "" || strings.TrimSpace(data.Version) == "" || len(data.Agents) < 2 || len(data.Cases) == 0 {
		return fmt.Errorf("routing dataset requires schema %q, identity, at least two agents, and cases", DatasetSchemaVersion)
	}
	agentIDs, caseIDs, splits, coverage := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index := range data.Agents {
		data.Agents[index] = domain.NormalizeAgentConfig(data.Agents[index])
		id := strings.TrimSpace(data.Agents[index].ID)
		if id == "" || agentIDs[id] {
			return fmt.Errorf("routing dataset has missing or duplicate agent id %q", id)
		}
		agentIDs[id] = true
	}
	for _, item := range data.Cases {
		if strings.TrimSpace(item.ID) == "" || caseIDs[item.ID] {
			return fmt.Errorf("routing dataset has missing or duplicate case id %q", item.ID)
		}
		caseIDs[item.ID], splits[item.Split] = true, true
		if strings.TrimSpace(item.Task) == "" || len(item.Coverage) == 0 {
			return fmt.Errorf("case %q requires a task and coverage tags", item.ID)
		}
		for _, tag := range item.Coverage {
			coverage[tag] = true
		}
		if item.Split != SplitCalibration && item.Split != SplitHoldout {
			return fmt.Errorf("case %q has invalid split %q", item.ID, item.Split)
		}
		if item.ExpectedOutcome != agent.AgentSelectionOutcomeSelected && item.ExpectedOutcome != agent.AgentSelectionOutcomeNoEligible && item.ExpectedOutcome != agent.AgentSelectionOutcomeNoSuitable {
			return fmt.Errorf("case %q has invalid expected outcome %q", item.ID, item.ExpectedOutcome)
		}
		if item.ExpectedOutcome == agent.AgentSelectionOutcomeSelected && len(item.AcceptableAgentIDs) == 0 {
			return fmt.Errorf("case %q requires acceptable agents", item.ID)
		}
		for _, id := range item.AcceptableAgentIDs {
			if !agentIDs[id] {
				return fmt.Errorf("case %q references unknown acceptable agent %q", item.ID, id)
			}
		}
	}
	if !splits[SplitCalibration] || !splits[SplitHoldout] {
		return errors.New("routing dataset requires frozen calibration and holdout splits")
	}
	for _, tag := range []string{"clear_single_specialist", "multiple_acceptable_specialists", "insufficient_capability", "no_match", "ambiguous_task", "candidate_description_conflict"} {
		if !coverage[tag] {
			return fmt.Errorf("routing dataset is missing required coverage %q", tag)
		}
	}
	return nil
}

func eligibleAgents(agents []domain.Agent, catalog *tools.Catalog, requirements domain.AgentRoutingRequirements) []domain.Agent {
	result := make([]domain.Agent, 0, len(agents))
	for _, item := range agent.EvaluateAgentRouting(agent.RoutingEvaluationInput{Agents: agents, Catalog: catalog, Task: "", Plan: "",
		Requirements: requirements, RouterMode: agent.RouterModeQuery}).Candidates {
		if item.Eligible {
			for _, candidate := range agents {
				if candidate.ID == item.AgentID {
					result = append(result, candidate)
					break
				}
			}
		}
	}
	return result
}

func metric(numerator, denominator int) Metric {
	result := Metric{Numerator: numerator, Denominator: denominator}
	if denominator > 0 {
		value := float64(numerator) / float64(denominator)
		result.Value = &value
	}
	return result
}

func isNoRoute(outcome string) bool {
	return outcome == agent.AgentSelectionOutcomeNoEligible || outcome == agent.AgentSelectionOutcomeNoSuitable
}
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func intersects(left, right []string) bool {
	for _, value := range left {
		if contains(right, value) {
			return true
		}
	}
	return false
}
func filterSamples(samples []Sample, split string) []Sample {
	result := []Sample{}
	for _, sample := range samples {
		if sample.Split == split {
			result = append(result, sample)
		}
	}
	return result
}
func estimateTokens(value string) int {
	if value == "" {
		return 0
	}
	return max(1, (len(value)+3)/4)
}
func normalizeRevision(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
func digest(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }
func uniqueSorted(values []int) []int {
	seen := map[int]bool{}
	result := []int{}
	for _, value := range values {
		if value >= 0 && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Ints(result)
	return result
}

func uniqueSortedFloats(values []float64) []float64 {
	seen := map[float64]bool{}
	result := make([]float64, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Float64s(result)
	return result
}

func agentCatalogHash(agents []domain.Agent) string {
	content, _ := json.Marshal(agents)
	return digest(content)
}
