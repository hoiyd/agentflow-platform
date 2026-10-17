package contexteval

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/redaction"
)

func runLiveSample(ctx context.Context, client *openai.Client, config domain.ContextAssemblyConfig, limits domain.RuntimeRunBudget, timeout time.Duration, c liveCase, s *LiveSample) (string, error) {
	f, err := newLiveFixture(ctx, client.RuntimeIdentity(), config, limits, c, s)
	if err != nil {
		return "", err
	}
	contractHash := liveDigest(f.catalog.Definitions())
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = budget.WithController(ctx, budget.NewTracker(f.store, nil, f.run))
	ctx = eventpkg.WithScope(ctx, eventpkg.Scope{RunID: f.run.ID, ConversationID: f.run.ConversationID})
	// A preflight of the complete baseline precedes either arm. Optional-history
	// dropping is not a valid full-history comparator, even if Assembly succeeds.
	full, err := client.PrepareAgentChat(f.session(ctx, nil), provider.ChatRequest{SystemPrompt: continuationSystem, History: f.history, Latest: f.input, Definitions: f.catalog.Definitions()})
	if err != nil && !errors.Is(err, contextassembly.ErrInputBudgetExceeded) {
		s.Status, s.Reason = "failed", liveErrorCode(err)
		return contractHash, captureLiveEvidence(f, s)
	}
	feasible := err == nil
	for _, e := range full.Manifest.Entries {
		if e.Source == contextassembly.SourceHistory && !e.Selected {
			feasible = false
		}
	}
	if !feasible {
		s.Status, s.Reason = "infeasible", "full_history_does_not_fit"
	} else {
		if s.Arm == StrategyCompacted {
			phase := f.phase(ctx, "compaction", domain.UsagePurposeCompaction, func(pctx context.Context) (string, error) {
				var compactErr error
				s.Compaction, compactErr = contextcompaction.NewCompactor(f.store).CompactIfNeeded(pctx, contextcompaction.Request{
					RunID: f.run.ID, ConversationID: f.run.ConversationID, Trigger: contextassembly.CompactionTriggerOverflow, TriggerKey: "evaluation-boundary", Config: config,
					Summarizer: contextcompaction.SummarizerFunc(func(sctx context.Context, request contextcompaction.SummaryRequest) (contextcompaction.SummaryResult, error) {
						sctx = contextassembly.WithSession(sctx, contextassembly.Session{Config: config, Sink: f.sink})
						result, e := client.CompleteTextDetailed(sctx, request.SystemPrompt, request.Prompt)
						return contextcompaction.SummaryResult{Text: result.Text, Model: result.Model}, e
					})})
				if s.Compaction == nil {
					if compactErr == nil {
						compactErr = errors.New("compaction did not produce a summary")
					}
					s.Fallback = true
					return "", compactErr
				}
				s.SummaryHash = liveDigest(s.Compaction.Summary)
				return s.Compaction.Summary, compactErr
			})
			s.Phases = append(s.Phases, phase)
		}
		// Probe without Task State, materials or protected recent context: this
		// measures summary loss independently of rescue during continuation.
		probe := f.phase(ctx, "fidelity_probe", domain.UsagePurposePrimary, func(pctx context.Context) (string, error) {
			var evidence strings.Builder
			if s.Compaction != nil {
				evidence.WriteString(s.Compaction.Summary)
			} else {
				for _, m := range f.history {
					evidence.WriteString(m.Content)
					evidence.WriteByte('\n')
				}
			}
			pctx = contextassembly.WithSession(pctx, contextassembly.Session{Config: config, Sink: f.sink})
			result, e := client.CompleteTextDetailed(pctx, probeSystem, "Requested fields: "+factFields(c)+"\nHistorical evidence:\n"+evidence.String())
			return result.Text, e
		})
		s.Phases = append(s.Phases, probe)
		s.Fidelity = checkClaims(probe.Output, c, s.SourceMessages)
		if probe.ErrorCode != "" {
			s.Fidelity.Passed = false
			s.Fidelity.Findings = append(s.Fidelity.Findings, probe.ErrorCode)
		}
		continued := f.phase(ctx, "continuation", domain.UsagePurposePrimary, func(pctx context.Context) (string, error) {
			pctx = f.session(pctx, s.Compaction)
			engine := turn.NewEngine(liveTurnModel{client: client, fixture: f})
			result, e := engine.Execute(pctx, turn.Request{RunID: f.run.ID, ConversationID: f.run.ConversationID, TurnID: budget.ScopeFromContext(pctx).TurnID,
				Input: f.input, History: f.history, SystemPrompt: continuationSystem, ModelMode: turn.ModelModeAgentStream, Catalog: f.catalog, Role: "evaluation", Sink: f.sink}, nil)
			return result.Output, e
		})
		s.Phases = append(s.Phases, continued)
		events, e := f.store.ListRunEvents(f.run.ID)
		if e != nil {
			return contractHash, e
		}
		s.MaterialReads = collectLiveReads(events)
		s.Outcome = checkContinuation(continued.Output, c, s.MaterialReads, s.Artifact.ID, s.SourceMessages)
		s.Status = "completed"
		for _, p := range s.Phases {
			if p.ErrorCode != "" {
				s.Status = "failed"
				s.Reason = p.ErrorCode
			}
		}
		if continued.ErrorCode != "" {
			s.Outcome.Passed = false
			s.Outcome.Findings = append(s.Outcome.Findings, continued.ErrorCode)
		}
	}
	if err := captureLiveEvidence(f, s); err != nil {
		return contractHash, err
	}
	return contractHash, nil
}

func captureLiveEvidence(f liveFixture, s *LiveSample) error {
	ledger, _, err := f.store.GetRunUsageLedger(f.run.ID)
	if err != nil {
		return err
	}
	s.Ledger = ledger
	for i, p := range s.Phases {
		entries := []domain.RunUsageEntry{}
		for _, e := range ledger.Entries {
			if e.TurnID == p.TurnID {
				entries = append(entries, e)
			}
		}
		s.Phases[i].Usage = budget.BuildLedger(f.run.ID, ledger.Budget, entries).Totals
		s.Phases[i].Output, _ = redaction.Text(p.Output)
	}
	events, err := f.store.ListRunEvents(f.run.ID)
	if err != nil {
		return err
	}
	for _, e := range events {
		s.Events = append(s.Events, LiveEvent{ID: e.ID, Type: e.Type, Sequence: e.Sequence, PayloadHash: liveDigest(e.Payload)})
		if e.Type == domain.EventContextAssembled {
			b, _ := json.Marshal(e.Payload["manifest"])
			var manifest domain.ContextManifest
			if err := json.Unmarshal(b, &manifest); err != nil {
				return err
			}
			s.Manifests = append(s.Manifests, manifest)
		}
	}
	current, err := f.store.ListMessages(f.run.ConversationID)
	if err != nil {
		return err
	}
	s.HistoryUnchanged = liveDigest(current) == liveDigest(f.history)
	if !s.HistoryUnchanged {
		s.Status, s.Reason = "failed", "source_history_modified"
	}
	status := domain.RunCompleted
	if s.Status != "completed" {
		status = domain.RunFailed
	}
	_, err = f.store.UpdateRunStatus(f.run.ID, status, s.Reason)
	return err
}

func liveErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return failure.Describe(err).Code
}
