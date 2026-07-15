package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrorKind identifies the recovery action for a failed model request.
type ErrorKind string

const (
	ErrorCanceled              ErrorKind = "canceled"
	ErrorTimeout               ErrorKind = "timeout"
	ErrorTransport             ErrorKind = "transport"
	ErrorRateLimited           ErrorKind = "rate_limited"
	ErrorProviderUnavailable   ErrorKind = "provider_unavailable"
	ErrorAuthentication        ErrorKind = "authentication"
	ErrorQuotaExceeded         ErrorKind = "quota_exceeded"
	ErrorModelNotFound         ErrorKind = "model_not_found"
	ErrorInvalidRequest        ErrorKind = "invalid_request"
	ErrorContextLengthExceeded ErrorKind = "context_length_exceeded"
	ErrorContentPolicy         ErrorKind = "content_policy"
	ErrorTokenBudgetExceeded   ErrorKind = "token_budget_exceeded"
	ErrorInvalidResponse       ErrorKind = "invalid_response"
)

// ModelError preserves provider details without exposing credentials or raw bodies.
type ModelError struct {
	Kind         ErrorKind
	Operation    string
	StatusCode   int
	ProviderType string
	ProviderCode string
	Message      string
	Retryable    bool
	RetryAfter   time.Duration
	Attempts     int
	Cause        error
}

func (e *ModelError) Error() string {
	if e == nil {
		return "model request failed"
	}
	parts := []string{fmt.Sprintf("kind=%s", e.Kind)}
	if e.Operation != "" {
		parts = append(parts, "operation="+e.Operation)
	}
	if e.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.StatusCode))
	}
	if e.ProviderCode != "" {
		parts = append(parts, "code="+e.ProviderCode)
	}
	if e.Attempts > 0 {
		parts = append(parts, fmt.Sprintf("attempts=%d", e.Attempts))
	}
	if e.Message != "" {
		parts = append(parts, "message="+e.Message)
	}
	return "model request failed: " + strings.Join(parts, " ")
}

func (e *ModelError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// AsModelError extracts the typed model failure from a wrapped error.
func AsModelError(err error) (*ModelError, bool) {
	var modelErr *ModelError
	ok := errors.As(err, &modelErr)
	return modelErr, ok
}

func classifyModelError(operation string, err error) *ModelError {
	if err == nil {
		return nil
	}
	if existing, ok := AsModelError(err); ok {
		copy := *existing
		if copy.Operation == "" {
			copy.Operation = operation
		}
		return &copy
	}
	if errors.Is(err, context.Canceled) {
		return &ModelError{Kind: ErrorCanceled, Operation: operation, Message: "request canceled", Cause: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &ModelError{Kind: ErrorTimeout, Operation: operation, Message: "request deadline exceeded", Retryable: true, Cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &ModelError{Kind: ErrorTimeout, Operation: operation, Message: "provider request timed out", Retryable: true, Cause: err}
	}
	return &ModelError{Kind: ErrorTransport, Operation: operation, Message: "provider connection failed", Retryable: true, Cause: err}
}

func modelErrorFromHTTPResponse(operation string, response *http.Response) *ModelError {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	providerType, providerCode, message := parseProviderError(body)
	kind, retryable := classifyHTTPFailure(response.StatusCode, providerType, providerCode, message)
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	message = publicModelErrorMessage(kind, message)
	return &ModelError{
		Kind:         kind,
		Operation:    operation,
		StatusCode:   response.StatusCode,
		ProviderType: providerType,
		ProviderCode: providerCode,
		Message:      cleanErrorMessage(message),
		Retryable:    retryable,
		RetryAfter:   parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
	}
}

func publicModelErrorMessage(kind ErrorKind, providerMessage string) string {
	switch kind {
	case ErrorAuthentication:
		return "provider authentication failed"
	case ErrorQuotaExceeded:
		return "provider quota exceeded"
	case ErrorContentPolicy:
		return "request blocked by provider content policy"
	default:
		return cleanErrorMessage(providerMessage)
	}
}

func invalidResponseError(operation, message string, cause error) *ModelError {
	return &ModelError{
		Kind: ErrorInvalidResponse, Operation: operation,
		Message: cleanErrorMessage(message), Retryable: true, Cause: cause,
	}
}

func withoutRetry(err error, operation string) *ModelError {
	modelErr := classifyModelError(operation, err)
	modelErr.Retryable = false
	return modelErr
}

func modelErrorMetadata(err error) map[string]any {
	modelErr, ok := AsModelError(err)
	if !ok {
		return nil
	}
	metadata := map[string]any{
		"error_kind": modelErr.Kind,
		"retryable":  modelErr.Retryable,
		"attempts":   modelErr.Attempts,
	}
	if modelErr.Operation != "" {
		metadata["operation"] = modelErr.Operation
	}
	if modelErr.ProviderType != "" {
		metadata["provider_type"] = modelErr.ProviderType
	}
	if modelErr.StatusCode > 0 {
		metadata["status_code"] = modelErr.StatusCode
	}
	if modelErr.ProviderCode != "" {
		metadata["provider_code"] = modelErr.ProviderCode
	}
	if modelErr.RetryAfter > 0 {
		metadata["retry_after_ms"] = modelErr.RetryAfter.Milliseconds()
	}
	return metadata
}

func addModelErrorMetadata(payload map[string]any, err error) map[string]any {
	for key, value := range modelErrorMetadata(err) {
		payload[key] = value
	}
	return payload
}

func parseProviderError(body []byte) (string, string, string) {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &envelope) == nil {
		message := strings.TrimSpace(envelope.Error.Message)
		if message == "" {
			message = strings.TrimSpace(envelope.Message)
		}
		return strings.TrimSpace(envelope.Error.Type), normalizeProviderCode(envelope.Error.Code), message
	}
	return "", "", strings.TrimSpace(string(body))
}

func normalizeProviderCode(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func classifyHTTPFailure(status int, providerType, providerCode, message string) (ErrorKind, bool) {
	fingerprint := strings.ToLower(strings.Join([]string{providerType, providerCode, message}, " "))
	switch {
	case containsAny(fingerprint, "insufficient_quota", "quota exceeded", "exceeded your current quota", "billing_hard_limit", "payment required", "insufficient credits", "credit balance") || status == http.StatusPaymentRequired:
		return ErrorQuotaExceeded, false
	case containsAny(fingerprint, "context_length_exceeded", "maximum context length", "context window"):
		return ErrorContextLengthExceeded, false
	case containsAny(fingerprint, "content_policy", "content policy", "content_filter", "safety policy"):
		return ErrorContentPolicy, false
	case containsAny(fingerprint, "model_not_found", "no such model", "model does not exist", "model is not available"):
		return ErrorModelNotFound, false
	case containsAny(fingerprint, "invalid_api_key", "authentication_error", "invalid authentication"):
		return ErrorAuthentication, false
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return ErrorAuthentication, false
	case status == http.StatusTooManyRequests:
		return ErrorRateLimited, true
	case status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooEarly:
		return ErrorTimeout, true
	case status >= http.StatusInternalServerError:
		return ErrorProviderUnavailable, true
	default:
		return ErrorInvalidRequest, false
	}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		return deadline.Sub(now)
	}
	return 0
}

func isStreamUsageUnsupported(err error) bool {
	return isInvalidRequestCapabilityError(err, "stream_options", "include_usage")
}

func isToolCallingUnsupported(err error) bool {
	return isInvalidRequestCapabilityError(err, "tool_choice", "tools", "function calling")
}

func isInvalidRequestCapabilityError(err error, markers ...string) bool {
	modelErr, ok := AsModelError(err)
	if !ok || modelErr.Kind != ErrorInvalidRequest {
		return false
	}
	fingerprint := strings.ToLower(strings.Join([]string{modelErr.ProviderType, modelErr.ProviderCode, modelErr.Message}, " "))
	return containsAny(fingerprint, markers...)
}

func containsAny(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func cleanErrorMessage(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	const maxMessageBytes = 1000
	if len(value) > maxMessageBytes {
		value = value[:maxMessageBytes]
		for len(value) > 0 && !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
