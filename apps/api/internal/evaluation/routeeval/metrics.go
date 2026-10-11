package routeeval

import (
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

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
	var inputTokenTotal, outputTokenTotal, tokenTotal, latencyTotal int64
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
		inputTokenTotal += int64(sample.InputTokens)
		outputTokenTotal += int64(sample.OutputTokens)
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
		result.MeanInputTokens = float64(inputTokenTotal) / float64(result.Evaluated)
		result.MeanOutputTokens = float64(outputTokenTotal) / float64(result.Evaluated)
		result.MeanRouterTokens = float64(tokenTotal) / float64(result.Evaluated)
		result.MeanLatencyMS = float64(latencyTotal) / float64(result.Evaluated)
	}
	return result
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
