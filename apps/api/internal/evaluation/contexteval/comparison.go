package contexteval

import (
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"strings"
)

func Compare(current, baseline Report, singleVariableAblation bool) Comparison {
	result := Comparison{Mode: "regression", Comparable: true, ChangedVariables: []string{}, Reasons: []string{}, Regressions: []string{}, Deltas: map[string]float64{}}
	if singleVariableAblation {
		result.Mode = "single_variable_ablation"
	}
	if baseline.ReportFormat != evalreport.Format || baseline.SchemaVersion != SchemaVersion {
		result.Reasons = append(result.Reasons, "report schema differs")
	}
	if current.DatasetID != baseline.DatasetID || current.DatasetVersion != baseline.DatasetVersion || current.DatasetHash != baseline.DatasetHash {
		result.Reasons = append(result.Reasons, "dataset identity differs")
	}
	if current.Config.Model != baseline.Config.Model || current.Config.AssemblerVersion != baseline.Config.AssemblerVersion || current.Config.CompactionAlgorithm != baseline.Config.CompactionAlgorithm || current.Config.Assembly != baseline.Config.Assembly {
		result.Reasons = append(result.Reasons, "assembly configuration differs")
	}
	if current.Config.Strategy != baseline.Config.Strategy {
		result.ChangedVariables = append(result.ChangedVariables, "strategy")
	}
	if len(result.ChangedVariables) > 0 && (!singleVariableAblation || len(result.ChangedVariables) != 1) {
		result.Reasons = append(result.Reasons, "context comparison requires identical inputs or one explicit strategy ablation")
	}
	result.Comparable = len(result.Reasons) == 0
	if !result.Comparable {
		return result
	}
	result.Deltas["required_fact_retention"] = current.Summary.RequiredFactRetention - baseline.Summary.RequiredFactRetention
	result.Deltas["forbidden_leak_rate"] = current.Summary.ForbiddenLeakRate - baseline.Summary.ForbiddenLeakRate
	result.Deltas["mean_input_tokens"] = current.Summary.MeanInputTokens - baseline.Summary.MeanInputTokens
	result.Deltas["mean_irrelevant_context_ratio"] = current.Summary.MeanIrrelevantRatio - baseline.Summary.MeanIrrelevantRatio
	if current.Summary.GatingFailures > baseline.Summary.GatingFailures {
		result.Regressions = append(result.Regressions, "gating failures increased")
	}
	if current.Summary.RequiredFactRetention < baseline.Summary.RequiredFactRetention {
		result.Regressions = append(result.Regressions, "required fact retention decreased")
	}
	if current.Summary.ForbiddenLeakRate > baseline.Summary.ForbiddenLeakRate {
		result.Regressions = append(result.Regressions, "forbidden content leakage increased")
	}
	if current.Summary.MeanIrrelevantRatio > baseline.Summary.MeanIrrelevantRatio {
		result.Regressions = append(result.Regressions, "irrelevant context ratio increased")
	}
	if current.Summary.PrefixSetHash != baseline.Summary.PrefixSetHash {
		result.Regressions = append(result.Regressions, "stable prefix changed")
	}
	return result
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
