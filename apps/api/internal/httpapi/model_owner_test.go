package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

func TestTrustedLocalRequestBindsModelOwner(t *testing.T) {
	h := &Handler{} // A nil OIDC manager denotes trusted-local mode.
	r := httptest.NewRequest(http.MethodPost, "/api/documents", nil)
	r.Header.Set("X-Owner-ID", "forged-owner")
	w := httptest.NewRecorder()
	h.withIdentity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if owner := requestcontrol.OwnerFromContext(r.Context()); owner != identity.SuperUserID {
			t.Fatalf("trusted-local model owner = %q, want %q", owner, identity.SuperUserID)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("local request status = %d", w.Code)
	}
}

// Both JSON dependency failures and already-open SSE must expose local overload,
// not a generic 500/provider fault. SSE cannot change its committed HTTP status.
func TestOwnerAdmissionHTTPFailures(t *testing.T) {
	for _, tc := range []struct {
		code   string
		status int
		retry  bool
	}{
		{"owner_model_queue_full", 429, true}, {"owner_model_queue_timeout", 503, true}, {"model_limiter_closed", 503, true},
		{"model_owner_required", 403, false}, {"model_owner_unavailable", 403, false},
	} {
		t.Run(tc.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", "/api/memories/search", nil)
			err := fmt.Errorf("embedding: %w", &requestcontrol.OwnerAdmissionError{Code: tc.code})
			writeFailure(w, r, 500, err)
			if w.Code != tc.status || (w.Header().Get("Retry-After") == "1") != tc.retry {
				t.Fatalf("status/headers: %d %v", w.Code, w.Header())
			}
			var body struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			}
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != tc.code || body.Retryable != tc.retry {
				t.Fatalf("body: %s", w.Body.String())
			}
			chunk := failureChatChunk(httptest.NewRecorder(), r, 500, err)
			data, _ := json.Marshal(chunk)
			var decoded map[string]any
			json.Unmarshal(data, &decoded)
			if chunk.ErrorCode != tc.code || (decoded["retry_after_ms"] == float64(1000)) != tc.retry {
				t.Fatalf("SSE: %s", data)
			}
		})
	}
}

func TestOwnerAdmissionFailureMappingLeavesOtherFailuresAlone(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	writeFailure(w, r, 500, fmt.Errorf("internal fixture failure"))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	var chunk domain.ChatChunk = failureChatChunk(w, r, 500, fmt.Errorf("internal fixture failure"))
	data, _ := json.Marshal(chunk)
	var decoded map[string]any
	json.Unmarshal(data, &decoded)
	if _, exists := decoded["retry_after_ms"]; exists {
		t.Fatal("invented retry evidence")
	}
}
