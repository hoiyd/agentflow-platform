package tooleval

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/evaluation/runtimehost"
)

type FileSkillOptions struct {
	runtimehost.Options
	DatasetPath      string  `json:"-"`
	SkillDirectory   string  `json:"-"`
	Revision         string  `json:"-"`
	EvidenceMode     string  `json:"evidence_mode"`
	Trials           int     `json:"trials"`
	MaxTokens        int     `json:"max_total_tokens"`
	SuiteTimeoutMS   int64   `json:"suite_timeout_ms"`
	MaxTokenRatio    float64 `json:"max_token_ratio"`
	MaxExtraRequests int     `json:"max_extra_requests"`
}

type FileSkillSample struct {
	Sample
	RunID             string           `json:"run_id"`
	ConversationID    string           `json:"conversation_id"`
	FileContent       string           `json:"file_content"`
	FileHash          string           `json:"file_hash"`
	OutcomeVerified   bool             `json:"outcome_verified"`
	SelectionVerified bool             `json:"selection_verified"`
	SafetyFailures    []string         `json:"safety_failures"`
	BoundaryHash      string           `json:"boundary_hash"`
	PhysicalRequests  int              `json:"physical_requests"`
	CostMicros        *int64           `json:"cost_micros"`
	Replay            domain.RunReplay `json:"replay"`
}

type FileSkillComparison struct {
	TaskID       string `json:"task_id"`
	Trial        int    `json:"trial"`
	Comparable   bool   `json:"comparable"`
	OutcomeDelta *int   `json:"outcome_delta"`
	TokenDelta   *int   `json:"token_delta"`
	RequestDelta *int   `json:"request_delta"`
}

type FileSkillReport struct {
	evalreport.Identity
	SchemaVersion    string                `json:"schema_version"`
	ExperimentAxis   string                `json:"experiment_axis"`
	SensorVersion    string                `json:"sensor_version"`
	Config           FileSkillOptions      `json:"config"`
	Dataset          FileSkillDataset      `json:"dataset"`
	Skill            domain.SkillSnapshot  `json:"skill"`
	Samples          []FileSkillSample     `json:"samples"`
	Summary          map[string]Summary    `json:"summary"`
	Comparisons      []FileSkillComparison `json:"comparisons"`
	PhysicalRequests int                   `json:"physical_requests"`
	TotalTokens      int                   `json:"total_tokens"`
	Gate             evalreport.Gate       `json:"gate"`
	Limitations      []string              `json:"limitations"`
}

func scoreFileSkill(data FileSkillDataset, task FileSkillTask, skill domain.SkillSnapshot, s *FileSkillSample) {
	s.SelectionVerified = true
	if s.Status != "completed" {
		s.Findings = append(s.Findings, "run_not_completed")
	}
	if strings.TrimSpace(s.Output) != task.ExpectedReply {
		s.Findings = append(s.Findings, "incorrect_final_reply")
	}
	if strings.TrimSpace(s.FileContent) != strings.TrimSpace(task.ExpectedFile) {
		s.Findings = append(s.Findings, "incorrect_persisted_file")
	}
	readSource, readOutput, wrote := false, false, false
	loads, reads := 0, 0
	for _, event := range s.Replay.RunEvents {
		if event.Type != domain.EventToolCompleted && event.Type != domain.EventToolFailed {
			continue
		}
		name, _ := event.Payload["tool_name"].(string)
		if event.Type == domain.EventToolFailed {
			code, _ := event.Payload["error_code"].(string)
			s.ToolFailures = append(s.ToolFailures, ToolFailure{Tool: name, Code: code})
			if code != "invalid_arguments" && code != "invalid_args" {
				s.SafetyFailures = append(s.SafetyFailures, "tool_failure:"+code)
			}
			continue
		}
		b, _ := json.Marshal(event.Payload["result"])
		var encoded string
		if json.Unmarshal(b, &encoded) == nil {
			b = []byte(encoded)
		}
		var result struct {
			Root, Path, Content string
			NewHash             string `json:"new_hash"`
			ContentHash         string `json:"content_hash"`
			Truncated           bool
		}
		_ = json.Unmarshal(b, &result)
		switch name {
		case "file_read":
			readSource = readSource || result.Root == "source" && result.Path == "brief.md" && result.Content == data.Material && result.ContentHash == fileContentHash(data.Material) && !result.Truncated
			readOutput = readOutput || wrote && result.Root == "output" && result.Path == "report.md" && result.Content == s.FileContent && result.ContentHash == s.FileHash && !result.Truncated
		case "file_write":
			call, _ := event.Payload["tool_call_id"].(string)
			committed := false
			for _, effect := range s.Replay.ToolEffects {
				if effect.ToolName == name && effect.ToolCallID == call && call != "" && effect.Status == domain.ToolEffectCommitted && effect.HasResult {
					committed = true
				}
			}
			wrote = wrote || readSource && committed && result.Root == "output" && result.Path == "report.md" && result.NewHash == s.FileHash
		case "skill_load":
			loads++
		case "skill_read":
			reads++
		default:
			s.SafetyFailures = append(s.SafetyFailures, "unexpected_tool:"+name)
		}
	}
	if task.ExpectedFile != "" && (!readSource || !readOutput || !wrote) {
		s.Findings = append(s.Findings, "missing_read_write_readback_evidence")
	}
	s.OutcomeVerified = s.Status == "completed" && len(s.Findings) == 0
	if task.ExpectedFile == "" && s.Usage.ToolCalls > 0 {
		s.SelectionVerified = false
	}
	if s.Arm == "with_skill" && task.ExpectSkill {
		included := false
		for _, e := range s.Replay.Projection.SkillEvidence {
			if e.Name != skill.Name || e.Hash != skill.Hash || !e.Bound || e.Instructions != "included" || e.RequestID == "" || e.ManifestID == "" {
				continue
			}
			for _, resource := range e.Resources {
				included = included || slices.ContainsFunc(skill.Resources, func(frozen domain.SkillResource) bool {
					return resource.Path == frozen.Path && resource.Hash == frozen.Hash && resource.Offset == 0 && resource.NextOffset == len(frozen.Content) && resource.TotalBytes == len(frozen.Content)
				})
			}
		}
		if !included || loads != 1 || reads < 1 {
			s.SelectionVerified = false
		}
	} else if loads > 0 || reads > 0 {
		s.SelectionVerified = false
	}
	if !s.SelectionVerified {
		s.Findings = append(s.Findings, "skill_selection_or_inclusion_mismatch")
	}
	for _, effect := range s.Replay.ToolEffects {
		if effect.Status != domain.ToolEffectCommitted && effect.Status != domain.ToolEffectFailed {
			s.SafetyFailures = append(s.SafetyFailures, "unsettled_file_effect")
		}
	}
	if len(s.SafetyFailures) > 0 {
		s.Findings = append(s.Findings, "safety_boundary_failure")
	}
	s.Verified = s.OutcomeVerified && s.SelectionVerified && len(s.SafetyFailures) == 0
	slices.Sort(s.Findings)
	slices.Sort(s.SafetyFailures)
}

func finishFileSkill(r *FileSkillReport) {
	type pairID struct {
		task  string
		trial int
	}
	base := map[pairID]FileSkillSample{}
	var common []Sample
	r.Comparisons = []FileSkillComparison{}
	r.Gate = evalreport.Gate{Passed: len(r.Samples) > 0, BlockingSamples: len(r.Samples), Reasons: []string{}}
	blocked := map[int]bool{}
	block := func(index int, reason string) {
		r.Gate.Passed = false
		blocked[index] = true
		s := r.Samples[index]
		r.Gate.Reasons = append(r.Gate.Reasons, fmt.Sprintf("%s/trial-%d/%s:%s", s.TaskID, s.Trial, s.Arm, reason))
	}
	for i, s := range r.Samples {
		common = append(common, s.Sample)
		if s.Arm == "without_skill" {
			base[pairID{s.TaskID, s.Trial}] = s
		}
		if len(s.SafetyFailures) > 0 || s.Status != "completed" || s.Usage.OpenReservations > 0 {
			block(i, "incomplete_or_unsafe")
		}
	}
	r.Summary = summarize(common)
	for i, s := range r.Samples {
		if s.Arm != "with_skill" {
			continue
		}
		b := base[pairID{s.TaskID, s.Trial}]
		pair := FileSkillComparison{TaskID: s.TaskID, Trial: s.Trial, Comparable: s.Status == "completed" && b.Status == "completed" && s.BoundaryHash != "" && s.BoundaryHash == b.BoundaryHash && s.Usage.OpenReservations == 0 && b.Usage.OpenReservations == 0}
		if pair.Comparable {
			d := 0
			if s.OutcomeVerified {
				d++
			}
			if b.OutcomeVerified {
				d--
			}
			pair.OutcomeDelta = &d
			t := s.Usage.TotalTokens - b.Usage.TotalTokens
			pair.TokenDelta = &t
			c := s.PhysicalRequests - b.PhysicalRequests
			pair.RequestDelta = &c
		}
		if !pair.Comparable || !s.Verified {
			block(i, "candidate_not_verified_or_comparable")
		} else if s.PhysicalRequests-b.PhysicalRequests > r.Config.MaxExtraRequests || float64(s.Usage.TotalTokens) > float64(max(1, b.Usage.TotalTokens))*r.Config.MaxTokenRatio {
			block(i, "cost_regression")
		}
		r.Comparisons = append(r.Comparisons, pair)
	}
	r.Gate.BlockingFailures = len(blocked)
	if len(r.Comparisons) == 0 {
		r.Gate.Passed = false
		r.Gate.Reasons = append(r.Gate.Reasons, "no_candidate_comparisons")
	}
}
