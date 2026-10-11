// Package contexteval provides deterministic regression checks for the final
// model input assembled from history and fixed compaction fixtures.
package contexteval

import (
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	SchemaVersion            = "context-quality-eval-v1"
	DatasetSchemaVersion     = "context-quality-dataset-v1"
	StrategyFullHistory      = "full_history"
	StrategyCompacted        = "compacted_history"
	ClassificationGating     = "gating"
	ClassificationDiagnostic = "diagnostic"
)

type Options struct {
	DatasetPath string
	Strategy    string
	Revision    string
}

func Run(ctx context.Context, opts Options) (Report, error) {
	if strings.TrimSpace(opts.DatasetPath) == "" {
		return Report{}, errors.New("context evaluation dataset path is required")
	}
	if opts.Strategy == "" {
		opts.Strategy = StrategyCompacted
	}
	if opts.Strategy != StrategyFullHistory && opts.Strategy != StrategyCompacted {
		return Report{}, fmt.Errorf("context strategy must be %q or %q", StrategyFullHistory, StrategyCompacted)
	}
	startedAt := time.Now().UTC()
	data, hash, err := loadDataset(opts.DatasetPath)
	if err != nil {
		return Report{}, err
	}
	config := contextassembly.NormalizeConfig(data.Config)
	report := Report{
		Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "offline_context_quality",
			DatasetID: data.ID, DatasetVersion: data.Version, DatasetHash: hash,
			GitRevision: normalizeRevision(opts.Revision), StartedAt: startedAt},
		SchemaVersion: SchemaVersion,
		Config: Config{Strategy: opts.Strategy, Model: data.Model, AssemblerVersion: contextassembly.AssemblerVersion,
			CompactionAlgorithm: contextcompaction.AlgorithmVersion, Assembly: config,
			FixtureScope: "fixed compaction summaries validate assembly and constraint retention, not summary generation quality"},
		CostSource: "not_applicable: deterministic assembly with no model request",
		Samples:    make([]Sample, 0, len(data.Cases)),
	}
	for _, item := range data.Cases {
		report.Samples = append(report.Samples, runSample(ctx, data.Model, config, opts.Strategy, item))
	}
	report.Summary = summarize(report.Samples)
	report.Gate = gate(report.Samples)
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func normalizeRevision(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
