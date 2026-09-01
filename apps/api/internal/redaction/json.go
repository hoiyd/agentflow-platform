package redaction

import (
	"encoding/json"
	"regexp"
	"strings"
)

const DeterministicStrategy = "deterministic-v1"

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

func valueWithSecretsRemoved(value any, count *int) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveKey(key) {
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
	regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`\b(?:ghp|github_pat)_[A-Za-z0-9_]{8,}\b`),
	regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|secret)\s*[:=]\s*[^\s,;]+`),
}

func stringWithSecretsRemoved(value string, count *int) string {
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllStringFunc(value, func(match string) string {
			(*count)++
			if index := strings.IndexAny(match, ":="); index >= 0 {
				return match[:index+1] + "[REDACTED]"
			}
			if strings.HasPrefix(strings.ToLower(match), "bearer ") {
				return "Bearer [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return value
}
