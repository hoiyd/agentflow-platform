package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/failure"
)

func TestWriteFailureRedactsInternalDetailsAndAddsRequestID(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/runs/run-secret/replay", nil)
	writeFailure(recorder, request, http.StatusInternalServerError, errors.New(`pq: column "secret_token" does not exist`))

	response := decodeAPIErrorResponse(t, recorder)
	if response.Error != http.StatusText(http.StatusInternalServerError) ||
		response.Code != "internal_error" || response.Category != string(failure.CategoryInternal) ||
		response.RequestID == "" || response.RequestID != recorder.Header().Get(RequestIDHeader) {
		t.Fatalf("unexpected internal error response: %#v", response)
	}
	if strings.Contains(recorder.Body.String(), "secret_token") || strings.Contains(recorder.Body.String(), "pq:") {
		t.Fatalf("internal details leaked in response: %s", recorder.Body.String())
	}
}

func TestWriteFailurePreservesStructuredClassification(t *testing.T) {
	err := failure.New(failure.Definition{
		Message: "raw provider response",
		Info: failure.Info{
			Code: "provider_unavailable", Source: "model_provider", Category: failure.CategoryAvailability,
			Retryable: true, Operation: "embedding",
		},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/rag/search", nil)
	writeFailure(recorder, request, http.StatusBadGateway, err)

	response := decodeAPIErrorResponse(t, recorder)
	if response.Error != http.StatusText(http.StatusBadGateway) || response.Code != "provider_unavailable" ||
		response.Source != "model_provider" || response.Category != string(failure.CategoryAvailability) ||
		!response.Retryable || response.Operation != "embedding" {
		t.Fatalf("unexpected classified error response: %#v", response)
	}
	if strings.Contains(recorder.Body.String(), "raw provider response") {
		t.Fatalf("provider details leaked in response: %s", recorder.Body.String())
	}
}

func TestWriteErrorReturnsStructuredValidationEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, http.StatusBadRequest, "message is required")

	response := decodeAPIErrorResponse(t, recorder)
	if response.Error != "message is required" || response.Code != "invalid_request" ||
		response.Source != "http_api" || response.Category != string(failure.CategoryValidation) || response.Retryable {
		t.Fatalf("unexpected validation response: %#v", response)
	}
}

func TestFailureInfoForHTTPStatusCoversStableClientClassifications(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		code      string
		category  failure.Category
		retryable bool
	}{
		{name: "unauthenticated", status: http.StatusUnauthorized, code: "unauthenticated", category: failure.CategoryAuthentication},
		{name: "forbidden", status: http.StatusForbidden, code: "forbidden", category: failure.CategoryAuthentication},
		{name: "not found", status: http.StatusNotFound, code: "not_found", category: failure.CategoryNotFound},
		{name: "conflict", status: http.StatusConflict, code: "conflict", category: failure.CategoryExecution},
		{name: "capacity", status: http.StatusTooManyRequests, code: "capacity_exceeded", category: failure.CategoryCapacity, retryable: true},
		{name: "availability", status: http.StatusServiceUnavailable, code: "service_unavailable", category: failure.CategoryAvailability, retryable: true},
		{name: "timeout", status: http.StatusGatewayTimeout, code: failure.CodeTimeout, category: failure.CategoryTimeout, retryable: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := failureInfoForHTTPStatus(test.status)
			if info.Code != test.code || info.Category != test.category || info.Retryable != test.retryable || info.Source != "http_api" {
				t.Fatalf("unexpected status classification: %#v", info)
			}
		})
	}
}

func TestFailureChatChunkUsesTheSameSafeContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	chunk := failureChatChunk(recorder, request, http.StatusInternalServerError, errors.New("database password leaked"))

	if chunk.Error != http.StatusText(http.StatusInternalServerError) || chunk.ErrorCode != "internal_error" ||
		chunk.ErrorCategory != string(failure.CategoryInternal) || chunk.RequestID == "" ||
		chunk.Retryable == nil || *chunk.Retryable {
		t.Fatalf("unexpected SSE failure chunk: %#v", chunk)
	}
}

func TestFormatHTTPFailureLogKeepsRawErrorAndCorrelationFields(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/rag/search?query=private", nil)
	info := failure.Info{
		Code: "provider_unavailable", Source: "model_provider", Category: failure.CategoryAvailability,
		Retryable: true, Operation: "embedding.openai_compatible",
	}
	line := formatHTTPFailureLog(
		request, "req_debug", "json", http.StatusBadGateway,
		errors.New("embedding provider returned upstream detail"), info,
	)

	for _, fragment := range []string{
		`request_id="req_debug"`, `transport="json"`, `method="POST"`, `path="/api/rag/search"`,
		`status=502`, `code="provider_unavailable"`, `source="model_provider"`,
		`category="availability"`, `retryable=true`, `operation="embedding.openai_compatible"`,
		`error="embedding provider returned upstream detail"`,
	} {
		if !strings.Contains(line, fragment) {
			t.Fatalf("failure log missing %q: %s", fragment, line)
		}
	}
	if strings.Contains(line, "query=private") {
		t.Fatalf("failure log should not include URL query values: %s", line)
	}
}

func decodeAPIErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder) apiErrorResponse {
	t.Helper()
	var response apiErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode API error response: %v body=%s", err, recorder.Body.String())
	}
	return response
}
