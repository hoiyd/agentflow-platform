// Package contexteval provides deterministic regression checks for the final
// model input assembled from history and fixed compaction fixtures.
package contexteval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evalreport"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/modelrequest"
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

type Config struct {
	Strategy            string                       `json:"strategy"`
	Model               string                       `json:"model"`
	AssemblerVersion    string                       `json:"assembler_version"`
	CompactionAlgorithm string                       `json:"compaction_algorithm"`
	Assembly            domain.ContextAssemblyConfig `json:"assembly"`
	FixtureScope        string                       `json:"fixture_scope"`
}

type ModelInputEvidence struct {
	PayloadHash          string         `json:"payload_hash"`
	PayloadBytes         int            `json:"payload_bytes"`
	MessageCount         int            `json:"message_count"`
	SourceTokenBreakdown map[string]int `json:"source_token_breakdown"`
}

type Sample struct {
	CaseID                 string              `json:"case_id"`
	Split                  string              `json:"split"`
	Classification         string              `json:"classification"`
	Scenario               string              `json:"scenario"`
	EvidencePosition       string              `json:"evidence_position,omitempty"`
	Status                 string              `json:"status"`
	Passed                 bool                `json:"passed"`
	RequiredFacts          int                 `json:"required_facts"`
	RetainedFacts          int                 `json:"retained_facts"`
	ForbiddenFacts         int                 `json:"forbidden_facts"`
	ForbiddenLeaks         int                 `json:"forbidden_leaks"`
	InputTokens            int                 `json:"input_tokens"`
	IrrelevantTokens       int                 `json:"irrelevant_tokens"`
	IrrelevantContextRatio float64             `json:"irrelevant_context_ratio"`
	SourceTokenShare       map[string]float64  `json:"source_token_share"`
	PrefixHash             string              `json:"prefix_hash,omitempty"`
	CompactionApplied      bool                `json:"compaction_applied"`
	RecoveryPath           string              `json:"recovery_path,omitempty"`
	ExpectedErrorCode      string              `json:"expected_error_code,omitempty"`
	ErrorCode              string              `json:"error_code,omitempty"`
	FailureReason          string              `json:"failure_reason,omitempty"`
	ModelInput             *ModelInputEvidence `json:"model_input,omitempty"`
}

type Summary struct {
	Samples               int            `json:"samples"`
	Evaluated             int            `json:"evaluated"`
	Passed                int            `json:"passed"`
	Failed                int            `json:"failed"`
	NotEvaluated          int            `json:"not_evaluated"`
	ExpectedErrors        int            `json:"expected_errors"`
	GatingSamples         int            `json:"gating_samples"`
	GatingFailures        int            `json:"gating_failures"`
	RequiredFacts         int            `json:"required_facts"`
	RetainedFacts         int            `json:"retained_facts"`
	RequiredFactRetention float64        `json:"required_fact_retention"`
	ForbiddenFacts        int            `json:"forbidden_facts"`
	ForbiddenLeaks        int            `json:"forbidden_leaks"`
	ForbiddenLeakRate     float64        `json:"forbidden_leak_rate"`
	MeanInputTokens       float64        `json:"mean_input_tokens"`
	MeanIrrelevantRatio   float64        `json:"mean_irrelevant_context_ratio"`
	SourceTokens          map[string]int `json:"source_tokens"`
	PrefixSetHash         string         `json:"prefix_set_hash"`
}

type Comparison struct {
	Mode             string             `json:"mode"`
	Comparable       bool               `json:"comparable"`
	ChangedVariables []string           `json:"changed_variables"`
	Reasons          []string           `json:"reasons"`
	Regressions      []string           `json:"regressions"`
	Deltas           map[string]float64 `json:"deltas"`
}

type Report struct {
	evalreport.Identity
	SchemaVersion string          `json:"schema_version"`
	Config        Config          `json:"config"`
	CostSource    string          `json:"cost_source"`
	Samples       []Sample        `json:"samples"`
	Summary       Summary         `json:"summary"`
	Gate          evalreport.Gate `json:"gate"`
	Comparison    *Comparison     `json:"comparison,omitempty"`
}

type dataset struct {
	SchemaVersion string                       `json:"schema_version"`
	ID            string                       `json:"id"`
	Version       string                       `json:"version"`
	Model         string                       `json:"model"`
	Config        domain.ContextAssemblyConfig `json:"config"`
	Cases         []evaluationCase             `json:"cases"`
}

type evaluationCase struct {
	ID                     string             `json:"id"`
	Split                  string             `json:"split"`
	Classification         string             `json:"classification"`
	Scenario               string             `json:"scenario"`
	EvidencePosition       string             `json:"evidence_position"`
	System                 string             `json:"system"`
	SystemRepeat           int                `json:"system_repeat,omitempty"`
	CurrentInput           string             `json:"current_input"`
	History                []fixtureMessage   `json:"history"`
	Compaction             *fixtureCompaction `json:"compaction,omitempty"`
	RequiredFacts          []string           `json:"required_facts"`
	ForbiddenFacts         []string           `json:"forbidden_facts"`
	IrrelevantReferenceIDs []string           `json:"irrelevant_reference_ids"`
	ExpectedErrorCode      string             `json:"expected_error_code,omitempty"`
}

type fixtureMessage struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type fixtureCompaction struct {
	Summary          string   `json:"summary"`
	SourceMessageIDs []string `json:"source_message_ids"`
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

func runSample(ctx context.Context, model string, config domain.ContextAssemblyConfig, strategy string, item evaluationCase) Sample {
	sample := Sample{CaseID: item.ID, Split: item.Split, Classification: item.Classification, Scenario: item.Scenario,
		EvidencePosition: item.EvidencePosition, Status: "completed", RequiredFacts: len(item.RequiredFacts),
		ForbiddenFacts: len(item.ForbiddenFacts), ExpectedErrorCode: item.ExpectedErrorCode,
		SourceTokenShare: map[string]float64{}}
	if err := ctx.Err(); err != nil {
		sample.Status, sample.ErrorCode, sample.FailureReason = "not_evaluated", failure.Describe(err).Code, "evaluation context canceled"
		return sample
	}
	history := make([]domain.Message, 0, len(item.History))
	for _, message := range item.History {
		history = append(history, domain.Message{ID: message.ID, Role: message.Role, Content: message.Content})
	}
	var compaction *domain.ContextCompaction
	if strategy == StrategyCompacted && item.Compaction != nil {
		compaction = &domain.ContextCompaction{ID: "fixture_" + item.ID, Status: domain.ContextCompactionCompleted,
			Generation: 1, Summary: item.Compaction.Summary, SourceMessageIDs: append([]string(nil), item.Compaction.SourceMessageIDs...),
			AlgorithmVersion: contextcompaction.AlgorithmVersion}
		sample.CompactionApplied = true
	}
	if item.Scenario == "compaction_failure_fallback" {
		compaction = nil
		sample.CompactionApplied = false
		sample.RecoveryPath = "raw_history_fallback"
	}
	system := item.System
	if item.SystemRepeat > 1 {
		system = strings.Repeat(item.System+" ", item.SystemRepeat)
	}
	request := contextassembly.Request{Model: model, Messages: []contextassembly.Message{
		{Source: contextassembly.SourceSystem, ReferenceID: "system", Role: "system", Content: system},
		{Source: contextassembly.SourceCurrentInput, ReferenceID: "current", Role: "user", Content: item.CurrentInput},
	}}
	pack, err := contextassembly.Assemble(contextassembly.WithSession(ctx, contextassembly.Session{
		Config: config, History: history, CurrentInput: item.CurrentInput, Compaction: compaction,
	}), request)
	sample.PrefixHash = pack.Manifest.PrefixHash
	fillTokenMetrics(&sample, pack.Manifest, item.IrrelevantReferenceIDs)
	if item.ExpectedErrorCode != "" {
		if err == nil {
			sample.Passed, sample.FailureReason = false, "expected error was not observed"
			return sample
		}
		sample.ErrorCode = failure.Describe(err).Code
		if sample.ErrorCode == item.ExpectedErrorCode {
			sample.Status, sample.Passed = "expected_error", true
			return sample
		}
		sample.Status, sample.FailureReason = "failed", "unexpected error code"
		return sample
	}
	if err != nil {
		sample.Status, sample.ErrorCode, sample.FailureReason = "failed", failure.Describe(err).Code, "context assembly failed"
		return sample
	}
	content := joinedContent(pack.Messages)
	for _, fact := range item.RequiredFacts {
		if strings.Contains(content, fact) {
			sample.RetainedFacts++
		}
	}
	for _, fact := range item.ForbiddenFacts {
		if strings.Contains(content, fact) {
			sample.ForbiddenLeaks++
		}
	}
	sample.Passed = sample.RetainedFacts == sample.RequiredFacts && sample.ForbiddenLeaks == 0
	if !sample.Passed {
		sample.FailureReason = "required fact missing or forbidden content included"
	}
	observation, err := observeModelInput(pack, request)
	if err != nil {
		sample.Status, sample.Passed, sample.ErrorCode, sample.FailureReason = "failed", false, "model_input_capture_failed", "model input evidence could not be encoded"
		return sample
	}
	digest := sha256.Sum256(observation.Payload)
	sample.ModelInput = &ModelInputEvidence{PayloadHash: hex.EncodeToString(digest[:]), PayloadBytes: len(observation.Payload),
		MessageCount: len(pack.Messages), SourceTokenBreakdown: observation.SourceTokenBreakdown}
	return sample
}

func observeModelInput(pack contextassembly.Pack, request contextassembly.Request) (modelrequest.Observation, error) {
	type message struct {
		Role       string          `json:"role"`
		Content    string          `json:"content,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
		ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	}
	payload := struct {
		Model    string                 `json:"model"`
		Messages []message              `json:"messages"`
		Tools    []contextassembly.Tool `json:"tools,omitempty"`
	}{Model: request.Model, Tools: request.Tools, Messages: make([]message, 0, len(pack.Messages))}
	for _, item := range pack.Messages {
		payload.Messages = append(payload.Messages, message{Role: item.Role, Content: item.Content, ToolCallID: item.ToolCallID, ToolCalls: item.ToolCalls})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return modelrequest.Observation{}, err
	}
	return modelrequest.Observation{ModelCallID: pack.Manifest.ModelCallID, Operation: "context.eval.preview", Provider: "offline", Model: request.Model,
		ContextManifestID: pack.Manifest.ID, SourceTokenBreakdown: sourceTokens(pack.Manifest), Payload: encoded}, nil
}

func fillTokenMetrics(sample *Sample, manifest domain.ContextManifest, irrelevantReferences []string) {
	sample.InputTokens = manifest.EstimatedInputTokens
	irrelevant := make(map[string]bool, len(irrelevantReferences))
	for _, reference := range irrelevantReferences {
		irrelevant[reference] = true
	}
	tokens := sourceTokens(manifest)
	for _, entry := range manifest.Entries {
		if entry.Selected && irrelevant[entry.ReferenceID] {
			sample.IrrelevantTokens += entry.EstimatedTokens
		}
	}
	if sample.InputTokens > 0 {
		sample.IrrelevantContextRatio = float64(sample.IrrelevantTokens) / float64(sample.InputTokens)
		for source, count := range tokens {
			sample.SourceTokenShare[source] = float64(count) / float64(sample.InputTokens)
		}
	}
}

func sourceTokens(manifest domain.ContextManifest) map[string]int {
	result := map[string]int{}
	for _, entry := range manifest.Entries {
		if entry.Selected {
			result[entry.Source] += entry.EstimatedTokens
		}
	}
	return result
}

func joinedContent(messages []contextassembly.Message) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(message.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}

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

func loadDataset(path string) (dataset, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return dataset{}, "", fmt.Errorf("read context dataset: %w", err)
	}
	if len(content) > 1<<20 {
		return dataset{}, "", errors.New("context dataset exceeds 1 MiB")
	}
	var data dataset
	if err := json.Unmarshal(content, &data); err != nil {
		return data, "", fmt.Errorf("decode context dataset: %w", err)
	}
	if err := validateDataset(data); err != nil {
		return data, "", err
	}
	sum := sha256.Sum256(content)
	return data, hex.EncodeToString(sum[:]), nil
}

func validateDataset(data dataset) error {
	if data.SchemaVersion != DatasetSchemaVersion || strings.TrimSpace(data.ID) == "" || strings.TrimSpace(data.Version) == "" || strings.TrimSpace(data.Model) == "" || len(data.Cases) == 0 {
		return errors.New("context dataset requires the supported schema, identity, model, and cases")
	}
	seen := map[string]bool{}
	gating := 0
	for _, item := range data.Cases {
		if strings.TrimSpace(item.ID) == "" || seen[item.ID] {
			return fmt.Errorf("context dataset contains an empty or duplicate case ID %q", item.ID)
		}
		seen[item.ID] = true
		if item.Split != "calibration" && item.Split != "holdout" {
			return fmt.Errorf("context case %q requires calibration or holdout split", item.ID)
		}
		if item.Classification != ClassificationGating && item.Classification != ClassificationDiagnostic {
			return fmt.Errorf("context case %q requires gating or diagnostic classification", item.ID)
		}
		if item.Classification == ClassificationGating {
			gating++
		}
		if strings.TrimSpace(item.System) == "" || strings.TrimSpace(item.CurrentInput) == "" {
			return fmt.Errorf("context case %q requires system and current input", item.ID)
		}
		if item.SystemRepeat < 0 || item.SystemRepeat > 100 {
			return fmt.Errorf("context case %q system_repeat must be 0-100", item.ID)
		}
		messageIDs := map[string]bool{}
		for _, message := range item.History {
			if strings.TrimSpace(message.ID) == "" || messageIDs[message.ID] || (message.Role != "user" && message.Role != "assistant") {
				return fmt.Errorf("context case %q contains an invalid history message", item.ID)
			}
			messageIDs[message.ID] = true
		}
		if item.Compaction != nil {
			if strings.TrimSpace(item.Compaction.Summary) == "" || len(item.Compaction.SourceMessageIDs) == 0 {
				return fmt.Errorf("context case %q contains an invalid compaction fixture", item.ID)
			}
			for _, id := range item.Compaction.SourceMessageIDs {
				if !messageIDs[id] {
					return fmt.Errorf("context case %q compaction references unknown message %q", item.ID, id)
				}
			}
		}
	}
	if gating == 0 {
		return errors.New("context dataset contains no gating cases")
	}
	return nil
}

func normalizeRevision(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
