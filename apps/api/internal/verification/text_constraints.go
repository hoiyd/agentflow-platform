package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

type TextConstraintsConfig struct {
	MinCharacters    int      `json:"min_characters,omitempty"`
	MaxCharacters    int      `json:"max_characters,omitempty"`
	MinWords         int      `json:"min_words,omitempty"`
	MaxWords         int      `json:"max_words,omitempty"`
	RequiredPhrases  []string `json:"required_phrases,omitempty"`
	ForbiddenPhrases []string `json:"forbidden_phrases,omitempty"`
	RequiredHeadings []string `json:"required_headings,omitempty"`
	CaseSensitive    bool     `json:"case_sensitive,omitempty"`
}

type textConstraintsVerifier struct{}

func (textConstraintsVerifier) Type() domain.VerifierType { return domain.VerifierTextConstraints }
func (textConstraintsVerifier) Version() string           { return "text-constraints-v1" }

func (textConstraintsVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	config, err := decodeConfig[TextConstraintsConfig](spec)
	if err != nil {
		return err
	}
	if config.MinCharacters < 0 || config.MaxCharacters < 0 || config.MinWords < 0 || config.MaxWords < 0 {
		return invalidContract("text_constraints verifier " + spec.ID + " limits must be non-negative")
	}
	if config.MaxCharacters > 0 && config.MinCharacters > config.MaxCharacters {
		return invalidContract("text_constraints verifier " + spec.ID + " min_characters exceeds max_characters")
	}
	if config.MaxWords > 0 && config.MinWords > config.MaxWords {
		return invalidContract("text_constraints verifier " + spec.ID + " min_words exceeds max_words")
	}
	config.RequiredPhrases = normalizedStrings(config.RequiredPhrases)
	config.ForbiddenPhrases = normalizedStrings(config.ForbiddenPhrases)
	config.RequiredHeadings = normalizedStrings(config.RequiredHeadings)
	if config.MinCharacters == 0 && config.MaxCharacters == 0 && config.MinWords == 0 && config.MaxWords == 0 &&
		len(config.RequiredPhrases) == 0 && len(config.ForbiddenPhrases) == 0 && len(config.RequiredHeadings) == 0 {
		return invalidContract("text_constraints verifier " + spec.ID + " requires at least one constraint")
	}
	return freezeConfig(spec, config)
}

func (textConstraintsVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, subject Subject) Result {
	config, err := decodeConfig[TextConstraintsConfig](&spec)
	if err != nil {
		return blocked(BlockedConfigInvalid, "text constraints config is invalid")
	}
	if err := ctx.Err(); err != nil {
		return blockedForContext(ctx, "text constraints verification was canceled")
	}

	characterCount := utf8.RuneCountInString(subject.Value)
	wordCount := len(strings.Fields(subject.Value))
	headings := markdownHeadings(subject.Value)
	violations := make([]string, 0)
	if characterCount < config.MinCharacters {
		violations = append(violations, fmt.Sprintf("character count %d is below minimum %d", characterCount, config.MinCharacters))
	}
	if config.MaxCharacters > 0 && characterCount > config.MaxCharacters {
		violations = append(violations, fmt.Sprintf("character count %d exceeds maximum %d", characterCount, config.MaxCharacters))
	}
	if wordCount < config.MinWords {
		violations = append(violations, fmt.Sprintf("word count %d is below minimum %d", wordCount, config.MinWords))
	}
	if config.MaxWords > 0 && wordCount > config.MaxWords {
		violations = append(violations, fmt.Sprintf("word count %d exceeds maximum %d", wordCount, config.MaxWords))
	}

	comparisonText := subject.Value
	comparisonHeadings := headings
	if !config.CaseSensitive {
		comparisonText = strings.ToLower(comparisonText)
		comparisonHeadings = lowerStrings(comparisonHeadings)
	}
	for _, phrase := range config.RequiredPhrases {
		if !strings.Contains(comparisonText, comparable(phrase, config.CaseSensitive)) {
			violations = append(violations, "missing required phrase: "+phrase)
		}
	}
	for _, phrase := range config.ForbiddenPhrases {
		if strings.Contains(comparisonText, comparable(phrase, config.CaseSensitive)) {
			violations = append(violations, "contains forbidden phrase: "+phrase)
		}
	}
	for _, heading := range config.RequiredHeadings {
		if !containsString(comparisonHeadings, comparable(heading, config.CaseSensitive)) {
			violations = append(violations, "missing required heading: "+heading)
		}
	}

	details := map[string]any{
		"character_count": characterCount,
		"word_count":      wordCount,
		"headings":        headings,
		"violations":      violations,
	}
	status := domain.VerificationPassed
	summary := "text constraints passed"
	if len(violations) > 0 {
		status = domain.VerificationFailed
		summary = fmt.Sprintf("text constraints failed with %d violation(s)", len(violations))
	}
	return Result{Status: status, Summary: summary, Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
}

func markdownHeadings(value string) []string {
	source := []byte(value)
	document := goldmark.DefaultParser().Parse(text.NewReader(source))
	headings := []string{}
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if heading, ok := node.(*ast.Heading); ok {
				headings = append(headings, strings.TrimSpace(string(heading.Text(source))))
			}
		}
		return ast.WalkContinue, nil
	})
	return headings
}

func normalizedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func lowerStrings(values []string) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strings.ToLower(value)
	}
	return result
}

func comparable(value string, caseSensitive bool) string {
	if caseSensitive {
		return value
	}
	return strings.ToLower(value)
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func diagnosticArtifact(details map[string]any) Artifact {
	content, _ := json.MarshalIndent(details, "", "  ")
	return Artifact{Kind: "diagnostic", MediaType: "application/json", Content: string(content), ByteSize: len(content)}
}
