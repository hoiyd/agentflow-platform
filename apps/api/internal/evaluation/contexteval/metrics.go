package contexteval

import (
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

func summarize(samples []Sample) Summary {
	result := Summary{Samples: len(samples), SourceTokens: map[string]int{}}
	completed := 0
	for _, sample := range samples {
		if sample.Classification == ClassificationGating {
			result.GatingSamples++
			if !sample.Passed || (sample.Status != "completed" && sample.Status != "expected_error") {
				result.GatingFailures++
			}
		}
		switch sample.Status {
		case "not_evaluated":
			result.NotEvaluated++
			continue
		case "failed":
			result.Failed++
			continue
		case "expected_error":
			result.ExpectedErrors++
			result.Evaluated++
			result.Passed++
			continue
		}
		result.Evaluated++
		if sample.Passed {
			result.Passed++
		} else {
			result.Failed++
		}
		completed++
		result.RequiredFacts += sample.RequiredFacts
		result.RetainedFacts += sample.RetainedFacts
		result.ForbiddenFacts += sample.ForbiddenFacts
		result.ForbiddenLeaks += sample.ForbiddenLeaks
		result.MeanInputTokens += float64(sample.InputTokens)
		result.MeanIrrelevantRatio += sample.IrrelevantContextRatio
		if sample.ModelInput != nil {
			for source, count := range sample.ModelInput.SourceTokenBreakdown {
				result.SourceTokens[source] += count
			}
		}
	}
	if result.RequiredFacts > 0 {
		result.RequiredFactRetention = float64(result.RetainedFacts) / float64(result.RequiredFacts)
	}
	if result.ForbiddenFacts > 0 {
		result.ForbiddenLeakRate = float64(result.ForbiddenLeaks) / float64(result.ForbiddenFacts)
	}
	if completed > 0 {
		result.MeanInputTokens /= float64(completed)
		result.MeanIrrelevantRatio /= float64(completed)
	}
	result.PrefixSetHash = prefixSetHash(samples)
	return result
}

func prefixSetHash(samples []Sample) string {
	items := make([]string, 0, len(samples))
	for _, sample := range samples {
		if sample.PrefixHash != "" {
			items = append(items, sample.CaseID+":"+sample.PrefixHash)
		}
	}
	sort.Strings(items)
	sum := sha256.Sum256([]byte(strings.Join(items, "\n")))
	return hex.EncodeToString(sum[:])
}

func gate(samples []Sample) evalreport.Gate {
	result := evalreport.Gate{Passed: true, Reasons: []string{}}
	for _, sample := range samples {
		if sample.Classification != ClassificationGating {
			continue
		}
		result.BlockingSamples++
		if !sample.Passed || (sample.Status != "completed" && sample.Status != "expected_error") {
			result.Passed = false
			result.BlockingFailures++
			reason := sample.FailureReason
			if reason == "" {
				reason = sample.ErrorCode
			}
			if reason == "" {
				reason = sample.Status
			}
			result.Reasons = append(result.Reasons, sample.CaseID+": "+reason)
		}
	}
	if result.BlockingSamples == 0 {
		result.Passed = false
		result.Reasons = append(result.Reasons, "dataset contains no gating samples")
	}
	return result
}
