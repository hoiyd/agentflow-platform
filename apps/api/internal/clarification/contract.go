// Package clarification owns the bounded question/answer contract. Execution,
// admission, Tool permissions and Run budgets remain with their existing owners.
package clarification

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool"
)

const ToolName = "request_user_input"
const MaxCheckpointBytes = 262144

// Conservative known credential topics are forbidden in clarification forms.
// This is not a semantic guarantee against disguised solicitation.
var credentialQuestion = regexp.MustCompile(`(?i)(\b(password|passphrase|api[ _-]?key|client[ _-]?secret|access[ _-]?token|private[ _-]?key|credentials)\b|密码|密钥|凭据|私钥|口令)`)

var ErrConflict = failure.New(failure.Definition{Message: "clarification is stale, expired, canceled or already applied", Info: failure.Info{Code: "clarification_conflict", Source: "clarification", Category: failure.CategoryValidation}})
var ErrInvalid = failure.New(failure.Definition{Message: "invalid clarification questions or answers", Info: failure.Info{Code: "clarification_invalid", Source: "clarification", Category: failure.CategoryValidation}})

// Pending is an internal control signal; it must never be treated as a failure.
var Pending = fmt.Errorf("clarification committed; execution paused")

func ValidateQuestions(questions []domain.ClarificationQuestion) error {
	if len(questions) < 1 || len(questions) > 3 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	bytes := 0
	for _, q := range questions {
		if credentialQuestion.MatchString(q.ID + " " + q.Question + " " + strings.Join(q.Options, " ")) {
			return ErrInvalid
		}
		if strings.TrimSpace(q.ID) == "" || len(q.ID) > 64 || seen[q.ID] || strings.TrimSpace(q.Question) == "" || !utf8.ValidString(q.Question) || len(q.Question) > 2000 || len(q.Options) > 6 {
			return ErrInvalid
		}
		seen[q.ID] = true
		bytes += len(q.ID) + len(q.Question)
		for _, r := range q.ID {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
				return ErrInvalid
			}
		}
		options := map[string]bool{}
		for _, v := range q.Options {
			if strings.TrimSpace(v) == "" || len(v) > 500 || options[v] || !utf8.ValidString(v) {
				return ErrInvalid
			}
			options[v] = true
			bytes += len(v)
		}
	}
	if bytes > 8192 {
		return ErrInvalid
	}
	return redaction.ValidateValue(questions)
}

func ValidateAnswers(questions []domain.ClarificationQuestion, answers map[string]string) error {
	if err := ValidateQuestions(questions); err != nil {
		return err
	}
	if len(answers) != len(questions) {
		return ErrInvalid
	}
	bytes := 0
	for _, q := range questions {
		v, ok := answers[q.ID]
		if !ok || strings.TrimSpace(v) == "" || !utf8.ValidString(v) {
			return ErrInvalid
		}
		bytes += len(v)
	}
	if bytes > 8192 {
		return ErrInvalid
	}
	return redaction.ValidateValue(answers)
}

func Binding() tool.Binding {
	text := func(max int) map[string]any {
		return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
	}
	id := text(64)
	id["pattern"] = "^[A-Za-z0-9_-]+$"
	question := tool.ObjectSchema(map[string]any{"id": id, "question": text(2000), "options": map[string]any{"type": "array", "maxItems": 6, "uniqueItems": true, "items": text(500)}}, []string{"id", "question"})
	return tool.Binding{Descriptor: tool.Descriptor{
		Name: ToolName, Description: "Ask 1-3 concrete questions only when necessary information is missing. Options are optional and free-text answers are always accepted. Call this tool alone, not with other tools. Execution pauses until the user responds; never request passwords, API keys or action approval.",
		Parameters:  tool.ObjectSchema(map[string]any{"questions": map[string]any{"type": "array", "minItems": 1, "maxItems": 3, "items": question}}, []string{"questions"}),
		Concurrency: tool.ConcurrencyPolicy{Mode: tool.ConcurrencySerial},
	}, Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
		var input struct {
			Questions []domain.ClarificationQuestion `json:"questions"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, err
		}
		if err := ValidateQuestions(input.Questions); err != nil {
			return nil, err
		}
		// The Binding only validates a control proposal. The application commits
		// questions and their protocol checkpoint atomically after the full batch.
		return input, nil
	}}
}
