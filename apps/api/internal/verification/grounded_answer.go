package verification

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
)

const defaultGroundedAnswerMinimumSupport = 0.50

var (
	groundingSentencePattern = regexp.MustCompile(`[.!?。！？]+(?:\s+|$)|\n+`)
	groundingCitationPattern = regexp.MustCompile(`(?i)\[S[0-9]+\]`)
	groundingAnchorPattern   = regexp.MustCompile("`([^`]+)`|\\b[A-Z][A-Za-z0-9_-]{2,}\\b")
)

var defaultNoAnswerPhrases = []string{
	"insufficient evidence", "not enough evidence", "cannot determine from the available",
	"can't determine from the available", "证据不足", "无法从现有资料", "无法根据现有资料",
}

var noAnswerClaimConnectors = []string{" but ", " however ", " yet ", "但是", "不过", "然而"}

type GroundedAnswerConfig struct {
	MinimumClaimSupport float64  `json:"minimum_claim_support,omitempty"`
	NoAnswerPhrases     []string `json:"no_answer_phrases,omitempty"`
}

type groundedAnswerVerifier struct{}

func (groundedAnswerVerifier) Type() domain.VerifierType { return domain.VerifierGroundedAnswer }
func (groundedAnswerVerifier) Version() string           { return "grounded-answer-lexical-v1" }

func (groundedAnswerVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	config, err := decodeConfig[GroundedAnswerConfig](spec)
	if err != nil {
		return err
	}
	if config.MinimumClaimSupport == 0 {
		config.MinimumClaimSupport = defaultGroundedAnswerMinimumSupport
	}
	if config.MinimumClaimSupport < 0.1 || config.MinimumClaimSupport > 1 {
		return invalidContract("grounded_answer verifier " + spec.ID + " minimum_claim_support must be between 0.1 and 1")
	}
	if len(config.NoAnswerPhrases) == 0 {
		config.NoAnswerPhrases = append([]string(nil), defaultNoAnswerPhrases...)
	}
	if len(config.NoAnswerPhrases) > 20 {
		return invalidContract("grounded_answer verifier " + spec.ID + " supports at most 20 no_answer_phrases")
	}
	for index := range config.NoAnswerPhrases {
		config.NoAnswerPhrases[index] = strings.ToLower(strings.TrimSpace(config.NoAnswerPhrases[index]))
		if config.NoAnswerPhrases[index] == "" {
			return invalidContract("grounded_answer verifier " + spec.ID + " no_answer_phrases cannot contain empty values")
		}
	}
	return freezeConfig(spec, config)
}

func (groundedAnswerVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, subject Subject) Result {
	config, err := decodeConfig[GroundedAnswerConfig](&spec)
	if err != nil {
		return blocked(BlockedConfigInvalid, "grounded answer config is invalid")
	}
	if err := ctx.Err(); err != nil {
		return blockedForContext(ctx, "grounded answer verification was canceled")
	}
	if subject.GroundingError != "" {
		return blocked(BlockedMissingInput, "grounding evidence is unavailable: "+subject.GroundingError)
	}

	sources := make(map[string]string, len(subject.Grounding))
	for _, source := range subject.Grounding {
		id := strings.ToUpper(strings.TrimSpace(source.SourceID))
		if id != "" && strings.TrimSpace(source.Content) != "" {
			sources[id] = source.Content
		}
	}
	claims, noAnswerCount := groundingClaims(subject.Value, config.NoAnswerPhrases)
	details := map[string]any{
		"algorithm": "lexical_claim_support", "minimum_claim_support": config.MinimumClaimSupport,
		"available_source_ids": sortedSourceIDs(sources), "claims_checked": len(claims),
		"insufficient_evidence_statements": noAnswerCount,
	}
	if len(sources) == 0 {
		if len(claims) == 0 && noAnswerCount > 0 {
			return Result{Status: domain.VerificationPassed, Summary: "grounded answer passed with an explicit insufficient-evidence response", Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
		}
		details["violations"] = []map[string]any{{"reason": "answer asserts claims without retrieved evidence"}}
		return Result{Status: domain.VerificationFailed, Summary: "grounded answer failed: no selected evidence supports the response", Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
	}
	if len(claims) == 0 {
		details["violations"] = []map[string]any{{"reason": "selected evidence exists but the response only claims insufficient evidence"}}
		return Result{Status: domain.VerificationFailed, Summary: "grounded answer failed: selected evidence was not used", Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
	}

	violations := make([]map[string]any, 0)
	for index, claim := range claims {
		markers := groundingCitationPattern.FindAllString(claim, -1)
		if len(markers) == 0 {
			violations = append(violations, groundingViolation(index, claim, "claim has no source marker", nil, 0, nil))
			continue
		}
		evidence := ""
		invalid := []string{}
		for _, marker := range markers {
			id := strings.ToUpper(strings.Trim(marker, "[]"))
			content, ok := sources[id]
			if !ok {
				invalid = append(invalid, id)
				continue
			}
			evidence += "\n" + content
		}
		if len(invalid) > 0 {
			violations = append(violations, groundingViolation(index, claim, "claim cites an unavailable source", markers, 0, invalid))
			continue
		}
		score, missingAnchors := claimSupport(claim, evidence)
		if score < config.MinimumClaimSupport || len(missingAnchors) > 0 {
			violations = append(violations, groundingViolation(index, claim, "cited source does not sufficiently support the claim", markers, score, missingAnchors))
		}
	}
	details["supported_claims"] = len(claims) - len(violations)
	if len(violations) == 0 {
		return Result{Status: domain.VerificationPassed, Summary: fmt.Sprintf("grounded answer passed with %d supported claim(s)", len(claims)), Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
	}
	details["violations"] = violations
	return Result{Status: domain.VerificationFailed, Summary: fmt.Sprintf("grounded answer failed with %d unsupported claim(s)", len(violations)), Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
}

func groundingClaims(answer string, noAnswerPhrases []string) ([]string, int) {
	parts := groundingSentencePattern.Split(strings.TrimSpace(answer), -1)
	claims := make([]string, 0, len(parts))
	noAnswerCount := 0
	for _, part := range parts {
		part = strings.TrimSpace(strings.TrimLeft(part, "#-* "))
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		if containsAnyPhrase(lower, noAnswerPhrases) && !containsAnyPhrase(lower, noAnswerClaimConnectors) {
			noAnswerCount++
			continue
		}
		if len(rag.QueryTerms(groundingCitationPattern.ReplaceAllString(part, ""))) == 0 {
			continue
		}
		claims = append(claims, part)
	}
	return claims, noAnswerCount
}

func claimSupport(claim, evidence string) (float64, []string) {
	cleanClaim := groundingCitationPattern.ReplaceAllString(claim, "")
	terms := rag.QueryTerms(cleanClaim)
	lowerEvidence := strings.ToLower(evidence)
	matched := 0
	for _, term := range terms {
		if strings.Contains(lowerEvidence, strings.ToLower(term)) {
			matched++
		}
	}
	score := 0.0
	if len(terms) > 0 {
		score = float64(matched) / float64(len(terms))
	}
	missingAnchors := []string{}
	seen := map[string]bool{}
	for _, match := range groundingAnchorPattern.FindAllStringSubmatch(cleanClaim, -1) {
		anchor := strings.TrimSpace(match[0])
		if match[1] != "" {
			anchor = strings.TrimSpace(match[1])
		}
		lower := strings.ToLower(anchor)
		if lower == "the" || lower == "this" || lower == "that" || seen[lower] {
			continue
		}
		seen[lower] = true
		if !strings.Contains(lowerEvidence, lower) {
			missingAnchors = append(missingAnchors, anchor)
		}
	}
	for _, term := range terms {
		if strings.IndexFunc(term, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 && !strings.Contains(lowerEvidence, strings.ToLower(term)) && !seen[strings.ToLower(term)] {
			missingAnchors = append(missingAnchors, term)
		}
	}
	return score, missingAnchors
}

func groundingViolation(index int, claim, reason string, sourceIDs []string, score float64, missing []string) map[string]any {
	if len(claim) > 240 {
		claim = claim[:240] + "..."
	}
	return map[string]any{"claim_index": index + 1, "claim": claim, "reason": reason, "source_ids": sourceIDs, "support_score": score, "missing_anchors": missing}
}

func sortedSourceIDs(sources map[string]string) []string {
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func containsAnyPhrase(value string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(value, strings.ToLower(strings.TrimSpace(phrase))) {
			return true
		}
	}
	return false
}
