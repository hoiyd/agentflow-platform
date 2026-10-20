package rageval

import (
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/rag"
	"fmt"
	"strings"
)

func Compare(current, baseline Report, singleVariableAblation bool) Comparison {
	comparison := Comparison{Mode: "regression", Comparable: true, ChangedVariables: []string{}, Reasons: []string{}, Regressions: []string{}, Deltas: map[string]float64{}}
	if singleVariableAblation {
		comparison.Mode = "single_variable_ablation"
	}
	if baseline.ReportFormat != evalreport.Format || baseline.SchemaVersion != SchemaVersion {
		comparison.Reasons = append(comparison.Reasons, "report schema differs")
	}
	if current.EvaluationKind != baseline.EvaluationKind {
		comparison.Reasons = append(comparison.Reasons, "evaluation kind differs")
	}
	if current.DatasetID != baseline.DatasetID || current.DatasetVersion != baseline.DatasetVersion || current.DatasetHash != baseline.DatasetHash {
		comparison.Reasons = append(comparison.Reasons, "dataset identity differs")
	}
	if current.Corpus != baseline.Corpus {
		comparison.Reasons = append(comparison.Reasons, "corpus identity differs")
	}
	comparison.ChangedVariables = changedVariables(current, baseline)
	if len(comparison.ChangedVariables) == 1 && comparison.ChangedVariables[0] == "reranker" && !sameCandidateInputs(current.Samples, baseline.Samples) {
		comparison.Reasons = append(comparison.Reasons, "reranker ablation requires identical recorded candidate inputs; regenerate missing baseline evidence")
	}
	if len(comparison.ChangedVariables) > 0 && (!singleVariableAblation || len(comparison.ChangedVariables) != 1) {
		comparison.Reasons = append(comparison.Reasons, "pipeline comparison requires identical inputs or exactly one explicit ablation variable")
	}
	comparison.Comparable = len(comparison.Reasons) == 0
	if !comparison.Comparable {
		return comparison
	}
	comparison.Deltas["hit_at_1"] = current.Summary.HitAt1 - baseline.Summary.HitAt1
	comparison.Deltas["hit_at_3"] = current.Summary.HitAt3 - baseline.Summary.HitAt3
	comparison.Deltas["hit_at_5"] = current.Summary.HitAt5 - baseline.Summary.HitAt5
	comparison.Deltas["mrr"] = current.Summary.MRR - baseline.Summary.MRR
	comparison.Deltas["ndcg"] = current.Summary.NDCG - baseline.Summary.NDCG
	comparison.Deltas["mean_latency_ms"] = current.Summary.MeanLatencyMS - baseline.Summary.MeanLatencyMS
	comparison.Deltas["leak_count"] = float64(current.Summary.LeakCount - baseline.Summary.LeakCount)
	comparison.Deltas["context_leak_count"] = float64(current.Summary.ContextLeakCount - baseline.Summary.ContextLeakCount)
	if current.Summary.GatingFailures > baseline.Summary.GatingFailures {
		comparison.Regressions = append(comparison.Regressions, "gating failures increased")
	}
	if current.Summary.MRR < baseline.Summary.MRR {
		comparison.Regressions = append(comparison.Regressions, "MRR decreased")
	}
	if current.Summary.NDCG < baseline.Summary.NDCG {
		comparison.Regressions = append(comparison.Regressions, "NDCG decreased")
	}
	if current.Summary.LeakCount > baseline.Summary.LeakCount {
		comparison.Regressions = append(comparison.Regressions, "forbidden-source leaks increased")
	}
	if current.Summary.ContextLeakCount > baseline.Summary.ContextLeakCount {
		comparison.Regressions = append(comparison.Regressions, "out-of-scope Context entries increased")
	}
	return comparison
}

func ApplyComparison(report *Report, baseline Report, singleVariableAblation bool) {
	comparison := Compare(*report, baseline, singleVariableAblation)
	report.Comparison = &comparison
	if !comparison.Comparable {
		report.Gate.Passed = false
		report.Gate.Reasons = append(report.Gate.Reasons, "baseline is not comparable: "+strings.Join(comparison.Reasons, ", "))
	} else if len(comparison.Regressions) > 0 {
		report.Gate.Passed = false
		report.Gate.Reasons = append(report.Gate.Reasons, comparison.Regressions...)
	}
}

func changedVariables(current, baseline Report) []string {
	changes := []string{}
	if current.Config.TopK != baseline.Config.TopK {
		changes = append(changes, "top_k")
	}
	if current.Config.MinSimilarity != baseline.Config.MinSimilarity {
		changes = append(changes, "min_similarity")
	}
	if current.Config.MinimumEvidenceCoverage != baseline.Config.MinimumEvidenceCoverage {
		changes = append(changes, "minimum_evidence_coverage")
	}
	if current.Config.Chunker != baseline.Config.Chunker {
		changes = append(changes, "chunker")
	}
	if normalizedRetrievalMode(current.Config.RetrievalMode) != normalizedRetrievalMode(baseline.Config.RetrievalMode) {
		changes = append(changes, "retrieval_mode")
	}
	if current.Pipeline.Embedding != baseline.Pipeline.Embedding || embeddingProfileIdentity(current) != embeddingProfileIdentity(baseline) {
		changes = append(changes, "embedding")
	}
	if current.Pipeline.Fusion != baseline.Pipeline.Fusion {
		changes = append(changes, "fusion")
	}
	if current.Pipeline.Reranker != baseline.Pipeline.Reranker {
		changes = append(changes, "reranker")
	}
	currentGate, baselineGate := current.Pipeline.RelevanceGate, baseline.Pipeline.RelevanceGate
	if current.Config.MinimumEvidenceCoverage != baseline.Config.MinimumEvidenceCoverage {
		currentGate.ConfigVersion, baselineGate.ConfigVersion = "", ""
		currentGate.MinimumEvidenceCoverage, baselineGate.MinimumEvidenceCoverage = 0, 0
	}
	if currentGate != baselineGate {
		changes = append(changes, "relevance_gate")
	}
	if current.Pipeline.SecurityPolicy != baseline.Pipeline.SecurityPolicy {
		changes = append(changes, "security_policy")
	}
	if current.Pipeline.EligibilityPolicy != baseline.Pipeline.EligibilityPolicy {
		changes = append(changes, "eligibility_policy")
	}
	return changes
}

func normalizedRetrievalMode(value string) string {
	if strings.TrimSpace(value) == "" {
		return rag.RetrievalModeHybrid
	}
	return strings.TrimSpace(value)
}

func embeddingProfileIdentity(report Report) string {
	name := strings.TrimSpace(report.EmbeddingProfile.Name)
	if name == "" && report.Pipeline.Embedding.Provider == "local" && report.Pipeline.Embedding.Model == "local_hash_embedding" {
		name = EmbeddingProfileHash
	}
	model := strings.TrimSpace(report.EmbeddingProfile.ConfiguredModel)
	if model == "" {
		model = report.Pipeline.Embedding.Model
	}
	dimensions := report.EmbeddingProfile.ConfiguredDimensions
	if dimensions == 0 {
		dimensions = report.Pipeline.Embedding.Dimensions
	}
	return fmt.Sprintf("%s/%s/%d", name, model, dimensions)
}
