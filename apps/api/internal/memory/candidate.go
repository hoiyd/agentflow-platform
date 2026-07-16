package memory

import (
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	CandidateReasonExplicit   = "explicit_memory_request"
	CandidateReasonPreference = "stable_preference"
	CandidateReasonCorrection = "user_correction"
	CandidateReasonConvention = "project_convention"

	PolicyAccepted          = "accepted"
	PolicyRejectRole        = "unsupported_source_role"
	PolicyRejectTooShort    = "content_too_short"
	PolicyRejectTooLong     = "content_too_long"
	PolicyRejectSecret      = "potential_secret"
	PolicyRejectTemporary   = "temporary_context"
	PolicyRejectTaskOutcome = "task_outcome"
)

type CandidateDraft struct {
	Kind             string
	Content          string
	ExtractionReason string
}

type CandidateExtractor interface {
	Extract(domain.Message) (CandidateDraft, bool)
}

type CandidatePolicy interface {
	Evaluate(domain.Message, CandidateDraft) PolicyDecision
}

type PolicyDecision struct {
	Accepted bool
	Reason   string
}

// RuleBasedCandidateExtractor intentionally favors precision over recall. It
// only proposes facts signaled as durable by the user; ordinary chat remains
// authoritative history and never becomes long-term memory implicitly.
type RuleBasedCandidateExtractor struct{}

type candidatePattern struct {
	prefix string
	kind   string
	reason string
}

var candidatePatterns = []candidatePattern{
	{prefix: "please remember that ", kind: "fact", reason: CandidateReasonExplicit},
	{prefix: "please remember ", kind: "fact", reason: CandidateReasonExplicit},
	{prefix: "remember that ", kind: "fact", reason: CandidateReasonExplicit},
	{prefix: "keep in mind that ", kind: "fact", reason: CandidateReasonExplicit},
	{prefix: "i prefer ", kind: "preference", reason: CandidateReasonPreference},
	{prefix: "my preference is ", kind: "preference", reason: CandidateReasonPreference},
	{prefix: "i always want ", kind: "preference", reason: CandidateReasonPreference},
	{prefix: "correction: ", kind: "correction", reason: CandidateReasonCorrection},
	{prefix: "for this project, ", kind: "project_convention", reason: CandidateReasonConvention},
	{prefix: "in this project, ", kind: "project_convention", reason: CandidateReasonConvention},
	{prefix: "project convention: ", kind: "project_convention", reason: CandidateReasonConvention},
	{prefix: "请记住", kind: "fact", reason: CandidateReasonExplicit},
	{prefix: "记住：", kind: "fact", reason: CandidateReasonExplicit},
	{prefix: "记住:", kind: "fact", reason: CandidateReasonExplicit},
	{prefix: "我的偏好是", kind: "preference", reason: CandidateReasonPreference},
	{prefix: "我更喜欢", kind: "preference", reason: CandidateReasonPreference},
	{prefix: "以后请", kind: "preference", reason: CandidateReasonPreference},
	{prefix: "更正：", kind: "correction", reason: CandidateReasonCorrection},
	{prefix: "更正:", kind: "correction", reason: CandidateReasonCorrection},
	{prefix: "本项目约定", kind: "project_convention", reason: CandidateReasonConvention},
	{prefix: "项目约定：", kind: "project_convention", reason: CandidateReasonConvention},
	{prefix: "项目约定:", kind: "project_convention", reason: CandidateReasonConvention},
}

func (RuleBasedCandidateExtractor) Extract(message domain.Message) (CandidateDraft, bool) {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return CandidateDraft{}, false
	}
	content := normalizeCandidateText(message.Content)
	for _, pattern := range candidatePatterns {
		if remainder, ok := trimPrefixFold(content, pattern.prefix); ok {
			remainder = strings.TrimSpace(strings.TrimLeft(remainder, ":：,- "))
			if remainder == "" {
				return CandidateDraft{}, false
			}
			return CandidateDraft{Kind: pattern.kind, Content: remainder, ExtractionReason: pattern.reason}, true
		}
	}
	return CandidateDraft{}, false
}

type ConservativeCandidatePolicy struct {
	MinRunes int
	MaxRunes int
}

func (p ConservativeCandidatePolicy) Evaluate(message domain.Message, draft CandidateDraft) PolicyDecision {
	if !strings.EqualFold(strings.TrimSpace(message.Role), "user") {
		return PolicyDecision{Reason: PolicyRejectRole}
	}
	minRunes := p.MinRunes
	if minRunes <= 0 {
		minRunes = 4
	}
	maxRunes := p.MaxRunes
	if maxRunes <= 0 {
		maxRunes = 1000
	}
	count := utf8.RuneCountInString(strings.TrimSpace(draft.Content))
	if count < minRunes {
		return PolicyDecision{Reason: PolicyRejectTooShort}
	}
	if count > maxRunes {
		return PolicyDecision{Reason: PolicyRejectTooLong}
	}
	lower := strings.ToLower(draft.Content)
	if containsAny(lower, secretMarkers) {
		return PolicyDecision{Reason: PolicyRejectSecret}
	}
	if containsAny(lower, temporaryMarkers) {
		return PolicyDecision{Reason: PolicyRejectTemporary}
	}
	if containsAny(lower, taskOutcomeMarkers) {
		return PolicyDecision{Reason: PolicyRejectTaskOutcome}
	}
	return PolicyDecision{Accepted: true, Reason: PolicyAccepted}
}

var secretMarkers = []string{
	"api key", "api_key", "apikey", "password", "passwd", "access token", "refresh token",
	"authorization:", "bearer ", "private key", "secret key", "sk-", "密码", "密钥", "令牌",
}

var temporaryMarkers = []string{
	"for this turn", "this turn only", "today only", "just this time", "temporarily", "right now",
	"本轮", "这一次", "仅这次", "临时", "今天", "现在先",
}

var taskOutcomeMarkers = []string{
	"task is complete", "task is completed", "finished the task", "work is done", "任务已完成", "已经完成任务",
}

func normalizeCandidateText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func trimPrefixFold(value, prefix string) (string, bool) {
	if len(value) < len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	return value[len(prefix):], true
}

func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
