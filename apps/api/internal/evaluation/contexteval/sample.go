package contexteval

import (
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

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

func observeModelInput(pack contextassembly.Pack, request contextassembly.Request) (requestcontrol.Observation, error) {
	payload := struct {
		Model    string                    `json:"model"`
		Messages []contextassembly.Message `json:"messages"`
		Tools    []contextassembly.Tool    `json:"tools,omitempty"`
	}{Model: request.Model, Tools: request.Tools, Messages: pack.Messages}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return requestcontrol.Observation{}, err
	}
	return requestcontrol.Observation{ModelCallID: pack.Manifest.ModelCallID, Operation: "context.eval.preview", Provider: "offline", Model: request.Model,
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
