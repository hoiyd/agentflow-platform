package tooleval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/toolartifact"
	"agentflow-platform/apps/api/internal/tools"
)

type Options struct {
	Trials         int           `json:"trials"`
	MaxModelCalls  int           `json:"max_model_calls"`
	MaxTotalTokens int           `json:"max_total_tokens"`
	Timeout        time.Duration `json:"sample_timeout_ns"`
	Revision       string        `json:"git_revision"`
}

type Sample struct {
	TaskID         string                `json:"task_id"`
	Trial          int                   `json:"trial"`
	Arm            string                `json:"arm"`
	Status         string                `json:"status"`
	Verified       bool                  `json:"verified"`
	Findings       []string              `json:"findings"`
	Output         string                `json:"output"`
	Error          string                `json:"error,omitempty"`
	LatencyMS      int64                 `json:"latency_ms"`
	Usage          domain.RunUsageTotals `json:"usage"`
	UsageEstimated bool                  `json:"usage_estimated"`
	Evidence       []Evidence            `json:"evidence"`
	ToolFailures   []ToolFailure         `json:"tool_failures"`
}

type ToolFailure struct {
	Tool string `json:"tool"`
	Code string `json:"code"`
}

type Summary struct {
	Samples        int     `json:"samples"`
	Evaluated      int     `json:"evaluated"`
	Verified       int     `json:"verified"`
	SuccessRate    float64 `json:"success_rate"`
	MeanTokens     float64 `json:"mean_tokens"`
	MeanToolCalls  float64 `json:"mean_tool_calls"`
	MeanModelCalls float64 `json:"mean_model_calls"`
	MeanLatencyMS  float64 `json:"mean_latency_ms"`
}

type Report struct {
	evalreport.Identity
	SchemaVersion    string                       `json:"schema_version"`
	Model            string                       `json:"model"`
	Provider         string                       `json:"provider"`
	Config           Options                      `json:"config"`
	ContextAssembly  domain.ContextAssemblyConfig `json:"context_assembly"`
	ToolContractHash string                       `json:"tool_contract_hash"`
	CostSource       string                       `json:"cost_source"`
	Samples          []Sample                     `json:"samples"`
	Summary          map[string]Summary           `json:"summary"`
	Gate             evalreport.Gate              `json:"gate"`
}

// One fixture owns the shared read-only corpus/catalog; each sample below gets
// a fresh Run and remaining budget. No state from prior model outputs is reused.
type evaluationFixture struct {
	store    *fixturestore.Store
	recorder *eventpkg.Recorder
	catalog  *tools.Catalog
	data     dataset
	assembly domain.ContextAssemblyConfig
}

// Run compares preview-only, full-context, and artifact-access inputs. It uses
// the production provider/Executor path, not full orchestration or a new Agent loop.
// The client must be dedicated to this evaluation (retries are disabled).
func Run(ctx context.Context, client *openai.Client, opts Options) (Report, error) {
	if client == nil || !client.HasAPIKey() {
		return Report{}, errors.New("explicit model credentials required; local fallback is not an evaluation")
	}
	if opts.Trials < 1 || opts.Trials > 20 || opts.MaxModelCalls < 1 || opts.MaxTotalTokens < 1 || opts.Timeout <= 0 || opts.Timeout > 5*time.Minute {
		return Report{}, errors.New("require 1-20 trials, positive suite call/token budgets and sample timeout <= 5m")
	}
	data, err := loadDataset()
	if err != nil {
		return Report{}, err
	}
	fs := fixturestore.New()
	recorder := eventpkg.NewRecorder(fs)
	catalog, err := tools.NewCatalog(toolartifact.NewService(fs, recorder).ToolBindings()...)
	if err != nil {
		return Report{}, err
	}
	definitions, err := json.Marshal(catalog.Definitions())
	if err != nil {
		return Report{}, err
	}
	assembly := contextassembly.DefaultConfig()
	assembly.OutputReserveTokens = 512
	assembly.CompactionMode = contextassembly.CompactionModeOff
	identity := client.RuntimeIdentity()
	startedAt := time.Now().UTC()
	report := Report{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "live_model", DatasetID: data.ID,
		DatasetVersion: data.Version, DatasetHash: data.Hash, GitRevision: opts.Revision, StartedAt: startedAt},
		SchemaVersion: "task-eval-v1", Model: identity.Model, Provider: identity.Provider, Config: opts,
		ContextAssembly: assembly, ToolContractHash: digest(string(definitions)), CostSource: "unavailable: no price table; token usage only",
		Samples: []Sample{}, Summary: map[string]Summary{}}
	client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
	fixture := evaluationFixture{fs, recorder, catalog, data, assembly}
	remainingCalls, remainingTokens := opts.MaxModelCalls, opts.MaxTotalTokens
	stop := ""
	for trial := 1; trial <= opts.Trials; trial++ {
		for _, task := range data.Cases {
			arms := []string{"without_tools", "full_context", "with_tools"}
			// Rotate order to reduce systematic latency/order bias across trials.
			if offset := (trial - 1) % len(arms); offset > 0 {
				arms = append(arms[offset:], arms[:offset]...)
			}
			for _, arm := range arms {
				sample := Sample{TaskID: task.ID, Trial: trial, Arm: arm, Findings: []string{}, Evidence: []Evidence{}, ToolFailures: []ToolFailure{}}
				if ctx.Err() != nil {
					stop = "canceled"
				}
				if remainingCalls <= 0 || remainingTokens <= 0 {
					stop = "suite_budget_exhausted"
				}
				if stop != "" {
					sample.Status, sample.Findings = "not_evaluated", []string{stop}
				} else {
					limits := domain.RuntimeRunBudget{MaxModelCalls: remainingCalls, MaxTotalTokens: remainingTokens, MaxToolCalls: 4, MaxRuntimeMS: opts.Timeout.Milliseconds()}
					if err := fixture.runSample(ctx, client, task, limits, opts.Timeout, &sample); err != nil {
						return report, err
					}
					remainingCalls -= sample.Usage.ModelCalls
					remainingTokens -= sample.Usage.TotalTokens
					// An unsettled request may have been billed beyond its reservation.
					// Do not spend more when remaining provider usage is unknown.
					if sample.Usage.OpenReservations > 0 {
						stop = "unsettled_model_usage"
					}
				}
				report.Samples = append(report.Samples, sample)
			}
		}
	}
	report.Summary = summarize(report.Samples)
	withTools := report.Summary["with_tools"]
	report.Gate = evalreport.Gate{Passed: withTools.Samples > 0 && withTools.Verified == withTools.Samples,
		BlockingSamples: withTools.Samples, BlockingFailures: withTools.Samples - withTools.Verified, Reasons: []string{}}
	if !report.Gate.Passed {
		report.Gate.Reasons = []string{"one or more Tool-arm samples were not verified"}
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func (f evaluationFixture) runSample(ctx context.Context, client *openai.Client, task Task, limits domain.RuntimeRunBudget, timeout time.Duration, sample *Sample) error {
	fs, content := f.store, f.data.Content
	conversation, err := fs.CreateConversation("Tool task evaluation")
	if err != nil {
		return err
	}
	run, err := fs.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &limits, ContextAssembly: f.assembly,
	}, nil)
	if err != nil {
		return err
	}
	artifact := domain.ToolArtifact{ID: "tool_artifact_" + run.ID, SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, ToolCallID: "fixture", ToolName: "settlement_export", MediaType: "text/plain",
		ContentHash: digest(content), OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: time.Now().UTC()}
	if _, err := fs.CreateToolArtifact(artifact, []byte(content)); err != nil {
		return err
	}
	active := f.catalog
	if sample.Arm != "with_tools" {
		active, err = tools.NewCatalog()
		if err != nil {
			return err
		}
	}
	input := fmt.Sprintf("Requested IDs: %s\nArtifact: %s\nPreview (not the full export):\n%s", strings.Join(task.IDs, ", "), artifact.ID, content[:256])
	if sample.Arm == "full_context" {
		input += "\nFull immutable export:\n" + content
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ctx = eventpkg.WithScope(ctx, eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: "evaluation", TurnID: "turn_" + run.ID})
	ctx = budget.WithController(ctx, budget.NewTracker(fs, nil, run))
	ctx = contextassembly.WithSession(ctx, contextassembly.Session{Config: f.assembly, CurrentInput: input})
	started := time.Now()
	events, errs := client.StreamAgentChatWithToolsTrace(ctx, systemPrompt, nil, input, active, f.recorder, run.ID, "evaluation", nil, nil)
	var output strings.Builder
	var executionErr error
	// Drain both channels so provider goroutines finish before temporary storage
	// is removed, including timeout/cancellation and partial-output paths.
	for events != nil || errs != nil {
		select {
		case item, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if item.Type == "delta" {
				output.WriteString(item.Delta)
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				executionErr = err
			}
		}
	}
	if executionErr == nil {
		executionErr = ctx.Err()
	}
	sample.LatencyMS = time.Since(started).Milliseconds()
	sample.Output, _ = redaction.Text(output.String())
	ledger, _, err := fs.GetRunUsageLedger(run.ID)
	if err != nil {
		return err
	}
	sample.Usage = ledger.Totals
	for _, entry := range ledger.Entries {
		if entry.Kind == domain.UsageModelSettlement && entry.Estimated {
			sample.UsageEstimated = true
		}
	}
	sample.UsageEstimated = sample.UsageEstimated || ledger.Totals.OpenReservations > 0
	runEvents, err := fs.ListRunEvents(run.ID)
	if err != nil {
		return err
	}
	sample.Evidence = collectEvidence(runEvents)
	for _, event := range runEvents {
		if event.Type == domain.EventToolFailed {
			tool, _ := event.Payload["tool_name"].(string)
			code, _ := event.Payload["error_code"].(string)
			sample.ToolFailures = append(sample.ToolFailures, ToolFailure{Tool: tool, Code: code})
		}
	}
	sample.Status = "completed"
	if executionErr != nil {
		sample.Status = "failed"
		sample.Error, _ = redaction.Text(executionErr.Error())
		sample.Findings = []string{failure.Describe(executionErr).Code}
		return nil
	}
	sample.Findings = verify(f.data, task, output.String(), artifact.ID, sample.Evidence, sample.Arm == "with_tools")
	sample.Verified = len(sample.Findings) == 0
	return nil
}

func summarize(samples []Sample) map[string]Summary {
	result := map[string]Summary{}
	for _, sample := range samples {
		summary := result[sample.Arm]
		summary.Samples++
		if sample.Verified {
			summary.Verified++
		}
		if sample.Status != "not_evaluated" {
			summary.Evaluated++
		}
		summary.MeanTokens += float64(sample.Usage.TotalTokens)
		summary.MeanToolCalls += float64(sample.Usage.ToolCalls)
		summary.MeanModelCalls += float64(sample.Usage.ModelCalls)
		summary.MeanLatencyMS += float64(sample.LatencyMS)
		result[sample.Arm] = summary
	}
	for arm, summary := range result {
		summary.SuccessRate = float64(summary.Verified) / float64(summary.Samples)
		// Keep skipped tasks in success-rate denominators, but do not pretend
		// they measured zero latency/token consumption.
		count := float64(max(1, summary.Evaluated))
		summary.MeanTokens /= count
		summary.MeanToolCalls /= count
		summary.MeanModelCalls /= count
		summary.MeanLatencyMS /= count
		result[arm] = summary
	}
	return result
}

// Passed is an explicit deterministic gate, not a claim of model reliability.
func (r Report) Passed() bool {
	return r.Gate.Passed
}
