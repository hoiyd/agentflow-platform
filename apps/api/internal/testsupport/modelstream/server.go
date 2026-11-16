package modelstream

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"agentflow-platform/apps/api/internal/inference/hashembedding"
)

// NewServer serves stateless wire fixtures. Callers own its lifetime.
func NewServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Input  string `json:"input"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/embeddings" {
			embedding, err := (hashembedding.Embedder{}).EmbedText(r.Context(), request.Input)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model": embedding.Model, "data": []any{map[string]any{"embedding": embedding.Vector}}})
			return
		}
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		const response = `{"choices":[{"message":{"role":"assistant","content":"Fixture answer: completed."},"finish_reason":"stop"}]}`
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			body, _ := Completion(response)
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(response))
	}))
}
