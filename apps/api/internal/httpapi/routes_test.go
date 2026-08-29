package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutesMatchOnlyDeclaredResourceShapes(t *testing.T) {
	handler := (&Handler{}).Routes()

	tests := []struct {
		name   string
		method string
		path   string
		status int
	}{
		{name: "health", method: http.MethodGet, path: "/health", status: http.StatusOK},
		{name: "unknown nested agent path", method: http.MethodGet, path: "/api/agents/agent-1/extra", status: http.StatusNotFound},
		{name: "unknown nested run path", method: http.MethodGet, path: "/api/runs/run-1/replay/extra", status: http.StatusNotFound},
		{name: "offline RAG evaluation is not exposed", method: http.MethodPost, path: "/api/rag/evaluations/run", status: http.StatusNotFound},
		{name: "memory administration is not exposed", method: http.MethodPost, path: "/api/memories", status: http.StatusNotFound},
		{name: "memory search administration is not exposed", method: http.MethodPost, path: "/api/memories/search", status: http.StatusNotFound},
		{name: "unsupported method", method: http.MethodPost, path: "/health", status: http.StatusNotFound},
		{name: "cors preflight", method: http.MethodOptions, path: "/api/chat", status: http.StatusNoContent},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("expected status %d, got %d body=%s", test.status, recorder.Code, recorder.Body.String())
			}
		})
	}
}
