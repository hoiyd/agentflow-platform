package rageval

import (
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

func summarizeSplits(samples []Sample) map[string]Summary {
	grouped := map[string][]Sample{}
	for _, sample := range samples {
		grouped[sample.Split] = append(grouped[sample.Split], sample)
	}
	summaries := make(map[string]Summary, len(grouped))
	for split, items := range grouped {
		summaries[split] = summarize(items)
	}
	return summaries
}

func evaluationSplit(tags []string) string {
	if contains(tags, "calibration") {
		return "calibration"
	}
	if contains(tags, "holdout") {
		return "holdout"
	}
	return "unspecified"
}

func summarize(samples []Sample) Summary {
	summary := Summary{Samples: len(samples)}
	var noAnswerTP, noAnswerFP, noAnswerFN int
	for _, sample := range samples {
		if sample.Classification == "gating" {
			summary.GatingSamples++
			if !sample.Passed || sample.Status != "completed" {
				summary.GatingFailures++
			}
		}
		if sample.Answerable {
			summary.AnswerableSamples++
		}
		if sample.PredictedNoAnswer && !sample.Answerable {
			noAnswerTP++
		}
		if sample.PredictedNoAnswer && sample.Answerable {
			noAnswerFP++
		}
		if !sample.PredictedNoAnswer && !sample.Answerable {
			noAnswerFN++
		}
		switch sample.Status {
		case "not_evaluated":
			summary.NotEvaluated++
			continue
		case "failed":
			summary.Failed++
			continue
		}
		summary.Evaluated++
		if sample.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		summary.LeakCount += sample.LeakCount
		summary.BlockedCandidates += sample.BlockedCandidates
		summary.MeanLatencyMS += float64(sample.LatencyMS)
		if sample.Answerable {
			summary.MRR += sample.ReciprocalRank
			summary.NDCG += sample.NDCG
			if sample.HitAt1 {
				summary.HitAt1++
			}
			if sample.HitAt3 {
				summary.HitAt3++
			}
			if sample.HitAt5 {
				summary.HitAt5++
			}
		}
	}
	if summary.AnswerableSamples > 0 {
		denominator := float64(summary.AnswerableSamples)
		summary.HitAt1 /= denominator
		summary.HitAt3 /= denominator
		summary.HitAt5 /= denominator
		summary.MRR /= denominator
		summary.NDCG /= denominator
	}
	if summary.Evaluated > 0 {
		summary.MeanLatencyMS /= float64(summary.Evaluated)
	}
	if denominator := noAnswerTP + noAnswerFP; denominator > 0 {
		value := float64(noAnswerTP) / float64(denominator)
		summary.NoAnswerPrecision = &value
	}
	if denominator := noAnswerTP + noAnswerFN; denominator > 0 {
		value := float64(noAnswerTP) / float64(denominator)
		summary.NoAnswerRecall = &value
	}
	return summary
}

func gate(samples []Sample) evalreport.Gate {
	result := evalreport.Gate{Passed: true, Reasons: []string{}}
	for _, sample := range samples {
		if sample.Classification != "gating" {
			continue
		}
		result.BlockingSamples++
		if sample.Status != "completed" || !sample.Passed {
			result.Passed = false
			result.BlockingFailures++
			result.Reasons = append(result.Reasons, sample.CaseID+": "+sampleFailure(sample))
		}
	}
	if result.BlockingSamples == 0 {
		result.Passed = false
		result.Reasons = append(result.Reasons, "dataset contains no gating samples")
	}
	return result
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sampleFailure(sample Sample) string {
	if sample.ErrorCode != "" {
		return sample.ErrorCode
	}
	if sample.FailureReason != "" {
		return sample.FailureReason
	}
	return sample.Status
}
