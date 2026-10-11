package routeeval

import (
	"agentflow-platform/apps/api/internal/agent"
	"math"
	"sort"
)

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
