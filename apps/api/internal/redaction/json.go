package redaction

import (
	"encoding/json"
	"io"
	"regexp"
	"strings"

	"agentflow-platform/apps/api/internal/failure"
)

const DeterministicStrategy = "deterministic-v2"

var ErrCredentialContent = failure.New(failure.Definition{
	Message: "credential-like content is not accepted",
	Info: failure.Info{
		Code: "credential_content_rejected", Source: "credential_boundary",
		Category: failure.CategoryValidation,
	},
})

// JSON removes common credential shapes from a JSON value while preserving a
// valid JSON document. Invalid input fails closed.
func JSON(payload []byte) ([]byte, int, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, 0, err
	}
	count := 0
	redacted, err := json.Marshal(valueWithSecretsRemoved(value, &count))
	return redacted, count, err
}

func Text(value string) (string, int) {
	count := 0
	return stringWithSecretsRemoved(value, &count), count
}

func Map(value map[string]any) (map[string]any, int) {
	count := 0
	redacted, _ := valueWithSecretsRemoved(value, &count).(map[string]any)
	return redacted, count
}

// ValidateText rejects credential-shaped user content before persistence or
// external processing. Callers keep the original content unchanged on success.
func ValidateText(values ...string) error {
	for _, value := range values {
		if _, count := Text(value); count > 0 {
			return ErrCredentialContent
		}
	}
	return nil
}

func ValidateValue(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, count, err := JSON(encoded)
	if err != nil {
		return err
	}
	if count > 0 {
		return ErrCredentialContent
	}
	return nil
}

// Writer removes credential values at the process log sink.
type Writer struct{ io.Writer }

func (w Writer) Write(payload []byte) (int, error) {
	if w.Writer == nil {
		return len(payload), nil
	}
	redacted, _ := Text(string(payload))
	if _, err := io.WriteString(w.Writer, redacted); err != nil {
		return 0, err
	}
	return len(payload), nil
}

func valueWithSecretsRemoved(value any, count *int) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveKey(key) {
				if text, ok := item.(string); ok && alreadyRedacted(text) {
					result[key] = item
					continue
				}
				result[key] = "[REDACTED]"
				(*count)++
				continue
			}
			result[key] = valueWithSecretsRemoved(item, count)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = valueWithSecretsRemoved(item, count)
		}
		return result
	case string:
		return stringWithSecretsRemoved(typed, count)
	default:
		return value
	}
}

func sensitiveKey(value string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_").Replace(strings.ToLower(strings.TrimSpace(value)))
	for _, marker := range []string{"api_key", "apikey", "authorization", "password", "secret", "token", "credential", "cookie"} {
		if normalized == marker || strings.HasSuffix(normalized, "_"+marker) {
			return true
		}
	}
	return false
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)\bbasic\s+[a-z0-9+/=]{8,}`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`\bgsk_[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`\b(?:ghp|github_pat)_[A-Za-z0-9_]{8,}\b`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{20,}\b`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{8,}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`(?i)\b(?:https?|postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis)://[^:\s/@]+:[^@\s/]+@`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|token|access[_-]?token|refresh[_-]?token|password|passwd|client[_-]?secret|secret[_-]?key|credential)\s*[:=]\s*[^\s,;]+`),
}

func stringWithSecretsRemoved(value string, count *int) string {
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllStringFunc(value, func(match string) string {
			if alreadyRedacted(match) {
				return match
			}
			(*count)++
			if strings.HasPrefix(match, "-----BEGIN") {
				return "[REDACTED]"
			}
			if index := strings.Index(match, "://"); index >= 0 {
				return match[:index+3] + "[REDACTED]@"
			}
			if index := strings.IndexAny(match, ":="); index >= 0 {
				return match[:index+1] + "[REDACTED]"
			}
			lower := strings.ToLower(match)
			if strings.HasPrefix(lower, "bearer ") {
				return "Bearer [REDACTED]"
			}
			if strings.HasPrefix(lower, "basic ") {
				return "Basic [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return value
}

func alreadyRedacted(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	return upper == "[REDACTED]" || strings.Contains(upper, "[REDACTED]")
}
