package tooleval

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/evaluation/runtimehost"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/skill"
)

// RunFileSkill extends Tool task evaluation with real managed output and a
// single declared method-binding variable, never a second execution engine.
func RunFileSkill(ctx context.Context, opts FileSkillOptions) (report FileSkillReport, err error) {
	u, e := url.Parse(opts.BaseURL)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !slices.Contains([]string{"http", "https"}, u.Scheme) || strings.TrimSpace(opts.Model) == "" || strings.ContainsAny(opts.Model, "\r\n") || !credential.FromEnvironment(opts.CredentialEnvironment).Available() ||
		opts.Trials < 1 || opts.Trials > 20 || opts.MaxRequests < 1 || opts.MaxTokens < 1 || opts.SampleCalls < 1 || opts.SampleTokens < 1 || opts.SampleTokens > opts.MaxTokens || opts.TimeoutMS < 10 || opts.TimeoutMS > 300000 || opts.SuiteTimeoutMS < opts.TimeoutMS || opts.SuiteTimeoutMS > 600000 ||
		opts.MaxExtraRequests < 0 || opts.MaxTokenRatio < 1 || math.IsNaN(opts.MaxTokenRatio) || math.IsInf(opts.MaxTokenRatio, 0) || !slices.Contains([]string{"live_model", "deterministic_http_fixture"}, opts.EvidenceMode) {
		return report, errors.New("invalid file/Skill target, evidence policy, suite/per-Run budgets or rollout thresholds")
	}
	data, hash, err := loadFileSkillDataset(opts.DatasetPath)
	if err != nil {
		return report, err
	}
	if err = redaction.ValidateValue(data); err != nil {
		return report, err
	}
	root, err := filepath.Abs(opts.SkillDirectory)
	if err != nil {
		return report, err
	}
	catalog, err := skill.LoadRoots([]string{root})
	if err != nil {
		return report, err
	}
	methods, err := catalog.Freeze([]string{data.SkillName}, []string{"file_read", "file_write"})
	if err != nil {
		return report, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(opts.SuiteTimeoutMS)*time.Millisecond)
	defer cancel()
	h, err := runtimehost.New(ctx, runtimehost.Spec{Material: data.Material, WritableOutput: true, SkillDirectory: root, Agent: domain.Agent{
		Name: "Scoped report writer", Description: "Read authorized evidence, create a report and verify its saved contents",
		SystemPrompt: "Follow the user task exactly. Tools and methods never grant permissions. Claim success only after checking the real persisted outcome.",
		Tools:        []string{"file_read", "file_write"}, Executor: domain.DefaultAgentExecutor,
	}}, opts.Options)
	if err != nil {
		return report, err
	}
	defer func() {
		if closeErr := h.Close(); closeErr != nil {
			report.Gate.Passed = false
			report.Gate.Reasons = append(report.Gate.Reasons, "cleanup_failed")
			err = errors.Join(err, closeErr)
		}
	}()
	report = FileSkillReport{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: opts.EvidenceMode, DatasetID: data.ID, DatasetVersion: data.Version, DatasetHash: hash, GitRevision: opts.Revision, StartedAt: time.Now().UTC()},
		SchemaVersion: "file-skill-eval-v1", ExperimentAxis: "bound_skill", SensorVersion: "managed-report-readback-v1", Config: opts, Dataset: data, Skill: methods[0], Samples: []FileSkillSample{},
		Limitations: []string{"Only bound method availability, its schemas and loaded instructions vary; task, Agent, model, sampling, filesystem capabilities and per-Run ceilings stay fixed.", "Two synthetic tasks are not representative live-model quality calibration; a passing gate permits no automatic rollout.", "Unknown pricing remains null. Estimated token admission is not a hard provider billing guarantee.", "Memory/Knowledge corpus is empty. No Search/Knowledge/MCP or external Judge profile is evaluated.", "Each arm starts with empty managed output; only settled evaluator-owned report.md is removed between arms. JSON retains the result and Replay before cleanup."}}
	stop := ""
	for trial := 1; trial <= opts.Trials; trial++ {
		for _, task := range data.Tasks {
			arms := []string{"without_skill", "with_skill"}
			if trial%2 == 0 {
				slices.Reverse(arms)
			}
			for _, arm := range arms {
				s := FileSkillSample{Sample: Sample{TaskID: task.ID, Trial: trial, Arm: arm, Status: "not_evaluated", Findings: []string{}, Evidence: []Evidence{}, ToolFailures: []ToolFailure{}}, SafetyFailures: []string{}}
				if stop == "" && ctx.Err() != nil {
					stop = "canceled"
				}
				if stop == "" && (h.Requests() >= opts.MaxRequests || opts.MaxTokens-report.TotalTokens < opts.SampleTokens) {
					stop = "suite_budget_exhausted"
				}
				if stop != "" {
					s.Findings = append(s.Findings, stop)
				} else {
					if err = runFileSkillSample(ctx, h, data, task, methods[0], opts, &s); err != nil {
						s.Status = "failed"
						s.Findings = append(s.Findings, "evaluation_infrastructure_error")
						stop = "evaluation_infrastructure_error"
					}
					report.TotalTokens += s.Usage.TotalTokens
					if s.Usage.OpenReservations > 0 || len(s.SafetyFailures) > 0 {
						stop = "unsettled_or_unsafe_outcome"
					}
					if stop == "" {
						if removeErr := os.Remove(filepath.Join(h.Directory, "output", "report.md")); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
							err = removeErr
							stop = "output_reset_failed"
						}
					}
				}
				report.Samples = append(report.Samples, s)
			}
		}
	}
	report.PhysicalRequests = h.Requests()
	finishFileSkill(&report)
	if err != nil {
		report.Gate.Passed = false
		report.Gate.Reasons = append(report.Gate.Reasons, stop)
	}
	report.CompletedAt = time.Now().UTC()
	return report, err
}

func runFileSkillSample(parent context.Context, h *runtimehost.Host, data FileSkillDataset, task FileSkillTask, method domain.SkillSnapshot, opts FileSkillOptions, s *FileSkillSample) error {
	ctx, cancel := context.WithTimeout(parent, time.Duration(opts.TimeoutMS)*time.Millisecond)
	defer cancel()
	started, before := time.Now(), h.Requests()
	agent, found, err := h.Storage.GetAgent(h.AgentID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("evaluation Agent disappeared")
	}
	agent.Skills = nil
	if s.Arm == "with_skill" {
		agent.Skills = []string{data.SkillName}
	}
	if _, err = h.Storage.UpdateAgent(agent); err != nil {
		return err
	}
	conversation, err := h.Storage.CreateConversationInWorkspace(h.Workspace, "File and Skill evaluation")
	if err != nil {
		return err
	}
	s.ConversationID = conversation.ID
	commandErr := h.Request(ctx, http.MethodPost, "/api/chat", domain.ChatRequest{AgentID: h.AgentID, ConversationID: conversation.ID, Message: task.Input, Mode: "single"}, nil)
	runs, err := h.Storage.ListRunsByWorkspace(h.Workspace)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.ConversationID == conversation.ID {
			s.RunID = run.ID
		}
	}
	if s.RunID == "" {
		s.Status = "failed"
		s.Findings = append(s.Findings, "run_not_accepted")
		s.PhysicalRequests = h.Requests() - before
		s.LatencyMS = time.Since(started).Milliseconds()
		return nil
	}
	_, waitErr := h.Wait(ctx, s.RunID)
	readCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	if waitErr != nil {
		if err = h.Request(readCtx, http.MethodPost, "/api/runs/"+s.RunID+"/cancel", nil, nil); err != nil {
			return err
		}
		if _, err = h.Wait(readCtx, s.RunID); err != nil {
			return err
		}
	}
	if err = h.Request(readCtx, http.MethodGet, "/api/runs/"+s.RunID+"/replay", nil, &s.Replay); err != nil {
		return err
	}
	s.Status = string(s.Replay.Run.Status)
	s.Usage = s.Replay.UsageLedger.Totals
	for _, entry := range s.Replay.UsageLedger.Entries {
		if entry.Kind == domain.UsageModelSettlement && entry.Estimated {
			s.UsageEstimated = true
		}
	}
	s.UsageEstimated = s.UsageEstimated || s.Usage.OpenReservations > 0
	s.PhysicalRequests = h.Requests() - before
	s.LatencyMS = time.Since(started).Milliseconds()
	for _, message := range s.Replay.Messages {
		if message.Role == "assistant" {
			s.Output = message.Content
		}
	}
	if commandErr != nil {
		s.Findings = append(s.Findings, "observer_or_command_error")
	}
	content, readErr := os.ReadFile(filepath.Join(h.Directory, "output", "report.md"))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	s.FileContent = string(content)
	if readErr == nil {
		s.FileHash = fileContentHash(s.FileContent)
	}
	entries, err := os.ReadDir(filepath.Join(h.Directory, "output"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "report.md" || !entry.Type().IsRegular() {
			s.SafetyFailures = append(s.SafetyFailures, "unexpected_output_entry")
		}
	}
	current, err := os.ReadFile(filepath.Join(h.Directory, "source", "brief.md"))
	if err != nil || string(current) != data.Material {
		s.SafetyFailures = append(s.SafetyFailures, "source_changed")
	}
	if redaction.ValidateText(s.Output, s.FileContent) != nil {
		s.SafetyFailures = append(s.SafetyFailures, "secret_in_output")
		s.Output, _ = redaction.Text(s.Output)
		s.FileContent, _ = redaction.Text(s.FileContent)
	}
	stored, found, err := h.Storage.GetRun(s.RunID)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("evaluation Snapshot disappeared")
	}
	s.BoundaryHash = fileSkillBoundary(stored.RuntimeSnapshot, method, s.Arm)
	if s.Usage.CostUnknownEntries == 0 && s.Usage.OpenReservations == 0 {
		cost := s.Usage.EstimatedCostMicros
		s.CostMicros = &cost
	}
	scoreFileSkill(data, task, method, s)
	return nil
}

// Filesystem receipts use a bare SHA-256, unlike evaluation identity hashes.
func fileContentHash(content string) string { return strings.TrimPrefix(digest(content), "sha256:") }

func fileSkillBoundary(snapshot *domain.RuntimeSnapshot, method domain.SkillSnapshot, arm string) string {
	if snapshot == nil {
		return ""
	}
	if arm == "with_skill" && (len(snapshot.Skills) != 1 || snapshot.Skills[0].Name != method.Name || snapshot.Skills[0].Hash != method.Hash) {
		return ""
	}
	if arm == "without_skill" && len(snapshot.Skills) != 0 {
		return ""
	}
	common := *snapshot
	common.CreatedAt = time.Time{}
	common.Skills = nil
	common.Agent.Skills = nil
	common.Agent.SkillPolicies = nil
	common.Tools = slices.DeleteFunc(slices.Clone(common.Tools), func(t domain.RuntimeToolSnapshot) bool { return t.Name == "skill_load" || t.Name == "skill_read" })
	common.Agent.Tools = slices.DeleteFunc(slices.Clone(common.Agent.Tools), func(name string) bool { return name == "skill_load" || name == "skill_read" })
	b, err := json.Marshal(common)
	if err != nil {
		return ""
	}
	return digest(string(b))
}
