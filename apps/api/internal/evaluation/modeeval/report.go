package modeeval

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

type Options struct {
	DatasetPath           string `json:"-"`
	DatabaseURL           string `json:"-"`
	BaseURL               string `json:"base_url"`
	Model                 string `json:"model"`
	CredentialEnvironment string `json:"credential_environment"`
	EvidenceMode          string `json:"evidence_mode"`
	Trials                int    `json:"trials"`
	MaxRequests           int    `json:"max_model_requests"`
	MaxTokens             int    `json:"max_total_tokens"`
	SampleCalls           int    `json:"sample_max_model_calls"`
	SampleTokens          int    `json:"sample_max_tokens"`
	TimeoutMS             int64  `json:"sample_timeout_ms"`
	SuiteTimeoutMS        int64  `json:"suite_timeout_ms"`
	ApprovalPolicy        string `json:"approval_policy"`
	Revision              string `json:"-"`
}

type Approval struct {
	Action        string `json:"action"`
	OriginalHash  string `json:"original_hash"`
	SubmittedHash string `json:"submitted_hash"`
	WaitMS        int64  `json:"wait_ms"`
}
type Sample struct {
	Trial                int                   `json:"trial"`
	Mode                 string                `json:"mode"`
	Status               string                `json:"status"`
	Verified             bool                  `json:"verified"`
	RunID                string                `json:"run_id"`
	ConversationID       string                `json:"conversation_id"`
	BoundaryHash         string                `json:"boundary_hash"`
	Findings             []string              `json:"findings"`
	MissingFacts         int                   `json:"missing_facts"`
	ConstraintViolations int                   `json:"constraint_violations"`
	FalseAcceptance      bool                  `json:"reviewed_but_unverified"`
	PhysicalRequests     int                   `json:"physical_requests"`
	Usage                domain.RunUsageTotals `json:"usage"`
	CostMicros           *int64                `json:"cost_micros"`
	ActiveMS             int64                 `json:"active_ms"`
	ApprovalWaitMS       int64                 `json:"approval_wait_ms"`
	WallMS               int64                 `json:"wall_ms"`
	Iterations           int                   `json:"iterations"`
	NoProgressIterations int                   `json:"no_progress_iterations"`
	StopReason           string                `json:"stop_reason"`
	StageMS              map[string]int64      `json:"stage_ms"`
	Approvals            []Approval            `json:"approvals"`
	Replay               domain.RunReplay      `json:"replay"`
}
type Summary struct {
	Samples      int     `json:"samples"`
	Verified     int     `json:"verified"`
	Failed       int     `json:"failed"`
	NotEvaluated int     `json:"not_evaluated"`
	SuccessRate  float64 `json:"success_rate"`
	P95MS        *int64  `json:"p95_ms"`
}
type Comparison struct {
	Trial         int    `json:"trial"`
	Mode          string `json:"mode"`
	Comparable    bool   `json:"comparable"`
	Reason        string `json:"reason,omitempty"`
	VerifiedDelta *int   `json:"verified_delta"`
	TokenDelta    *int   `json:"total_token_delta"`
	RequestDelta  *int   `json:"physical_request_delta"`
	ActiveDeltaMS *int64 `json:"active_delta_ms"`
}
type Report struct {
	evalreport.Identity
	SchemaVersion    string             `json:"schema_version"`
	ExperimentAxis   string             `json:"experiment_axis"`
	SensorVersion    string             `json:"sensor_version"`
	Options          Options            `json:"config"`
	Dataset          Dataset            `json:"dataset"`
	PhysicalRequests int                `json:"physical_requests"`
	TotalTokens      int                `json:"total_tokens"`
	Samples          []Sample           `json:"samples"`
	Summary          map[string]Summary `json:"summary"`
	Comparisons      []Comparison       `json:"comparisons"`
	Gate             evalreport.Gate    `json:"gate"`
	Limitations      []string           `json:"limitations"`
}

func score(data Dataset, sample *Sample) {
	var answer string
	for _, m := range sample.Replay.Messages {
		if m.Role == "assistant" {
			answer = m.Content
		}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(strings.TrimSpace(answer)), &fields) != nil {
		sample.Findings = append(sample.Findings, "invalid_output_json")
	}
	for key := range fields {
		if _, ok := data.Expected[key]; !ok {
			sample.ConstraintViolations++
			sample.Findings = append(sample.Findings, "unexpected_output_field_"+key)
		}
	}
	for key, expected := range data.Expected {
		var actual, want any
		_ = json.Unmarshal(fields[key], &actual)
		_ = json.Unmarshal(expected, &want)
		if !reflect.DeepEqual(actual, want) {
			sample.MissingFacts++
			sample.Findings = append(sample.Findings, "incorrect_or_missing_"+key)
		}
	}
	read := false
	for _, event := range sample.Replay.RunEvents {
		if event.Type != domain.EventToolCompleted {
			continue
		}
		name, _ := event.Payload["tool_name"].(string)
		if name != "file_read" {
			sample.ConstraintViolations++
			continue
		}
		b, _ := json.Marshal(event.Payload["result"])
		// Tool results are encoded JSON strings in the shared tracing contract.
		var encoded string
		if json.Unmarshal(b, &encoded) == nil {
			b = []byte(encoded)
		}
		var result struct {
			Root, Path  string
			ContentHash string `json:"content_hash"`
			Content     string
			Truncated   bool
		}
		if json.Unmarshal(b, &result) == nil && result.Root == "source" && result.Path == "brief.md" && result.ContentHash == contentHash(data.Material) && result.Content == data.Material && !result.Truncated {
			read = true
		}
	}
	if !read {
		sample.Findings = append(sample.Findings, "missing_authorized_read_receipt")
	}
	if sample.ConstraintViolations > 0 {
		sample.Findings = append(sample.Findings, "task_constraint_violation")
	}
	previous := ""
	reviewed := false
	for _, step := range sample.Replay.Steps {
		if step.Status != domain.CollaborationStepCompleted {
			continue
		}
		if step.Role == "act" {
			if previous != "" && previous == step.Output {
				sample.NoProgressIterations++
			}
			previous = step.Output
		}
		if step.Role == "reviewer" || step.Role == "review" {
			reviewed = true
		}
	}
	sample.Verified = sample.Status == "completed" && len(sample.Findings) == 0
	sample.FalseAcceptance = reviewed && sample.Status == "completed" && !sample.Verified
	sort.Strings(sample.Findings)
}

func summarize(report *Report) {
	report.Summary = map[string]Summary{}
	report.Gate = evalreport.Gate{Passed: true, BlockingSamples: len(report.Samples), Reasons: []string{}}
	baselines := map[int]Sample{}
	for _, s := range report.Samples {
		if s.Mode == "single" {
			baselines[s.Trial] = s
		}
	}
	for _, s := range report.Samples {
		summary := report.Summary[s.Mode]
		summary.Samples++
		if s.Verified {
			summary.Verified++
		}
		if s.Status == "not_evaluated" {
			summary.NotEvaluated++
		}
		if s.Status != "completed" && s.Status != "not_evaluated" {
			summary.Failed++
		}
		summary.SuccessRate = float64(summary.Verified) / float64(summary.Samples)
		report.Summary[s.Mode] = summary
		if s.Status != "completed" || s.Usage.OpenReservations > 0 {
			report.Gate.Passed = false
			report.Gate.BlockingFailures++
			reason := s.StopReason
			if s.Usage.OpenReservations > 0 {
				reason = "unsettled_model_usage"
			}
			report.Gate.Reasons = append(report.Gate.Reasons, s.Mode+": "+reason)
		}
		if s.Mode == "single" {
			continue
		}
		base := baselines[s.Trial]
		pair := Comparison{Trial: s.Trial, Mode: s.Mode}
		if base.Status != "completed" || s.Status != "completed" || base.BoundaryHash == "" || base.BoundaryHash != s.BoundaryHash || base.Usage.OpenReservations > 0 || s.Usage.OpenReservations > 0 {
			pair.Reason = "incomplete arms, unsettled usage or common contract drift"
			report.Gate.Passed = false
			if base.Status == "completed" && s.Status == "completed" && s.Usage.OpenReservations == 0 {
				report.Gate.BlockingFailures++
			}
			report.Gate.Reasons = append(report.Gate.Reasons, s.Mode+": "+pair.Reason)
		} else {
			pair.Comparable = true
			v := 0
			if s.Verified {
				v++
			}
			if base.Verified {
				v--
			}
			pair.VerifiedDelta = &v
			tokens := s.Usage.TotalTokens - base.Usage.TotalTokens
			requests := s.PhysicalRequests - base.PhysicalRequests
			active := s.ActiveMS - base.ActiveMS
			if s.Usage.OpenReservations == 0 && base.Usage.OpenReservations == 0 {
				pair.TokenDelta = &tokens
				pair.RequestDelta = &requests
				pair.ActiveDeltaMS = &active
			}
		}
		report.Comparisons = append(report.Comparisons, pair)
	}
}
