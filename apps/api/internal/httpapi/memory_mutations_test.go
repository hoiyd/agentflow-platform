package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

func TestMemoryMutationAPI(t *testing.T) {
	s, err := store.NewFileStore(t.TempDir() + "/memory.json")
	if err != nil {
		t.Fatal(err)
	}
	p := memorypkg.NewBuiltinProvider(s, newLocalFallbackOpenAIClientForTest(), memorypkg.ProviderOptions{MaxAttempts: 1})
	if err := p.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer closeMemoryProvider(t, p)
	m, err := p.Commit(context.Background(), domain.Memory{Kind: "fact", Content: "old"})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{memories: p}
	mux := http.NewServeMux()
	h.registerMemoryRoutes(mux)
	call := func(method, path, body, workspace string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set(WorkspaceHeader, workspace)
		w := httptest.NewRecorder()
		h.withWorkspace(mux).ServeHTTP(w, r)
		return w
	}
	url := "/api/memories/" + m.ID
	body := `{"operation_id":"op","expected_version":1,"action":"replace","content":"new","actor":"user","reason":"correction"}`
	w := call("POST", url+"/mutations", body, "")
	if w.Code != 200 {
		t.Fatalf("replace %d %s", w.Code, w.Body)
	}
	var result domain.MemoryMutationResult
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || !result.Applied || result.Memory.Version != 2 {
		t.Fatalf("contract: %+v %v", result, err)
	}
	w = call("POST", url+"/mutations", body, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"applied":false`) {
		t.Fatalf("retry %s", w.Body)
	}
	for _, tc := range []struct {
		name, method, path, body, workspace string
		status                              int
	}{
		{"detail", "GET", url, "", "", 200},
		{"scope-read", "GET", url, "", "other", 404},
		{"scope-write", "POST", url + "/mutations", body, "other", 404},
		{"stale", "POST", url + "/mutations", strings.Replace(body, `"op"`, `"other"`, 1), "", 409},
		{"altered-command", "POST", url + "/mutations", strings.Replace(body, `"new"`, `"different"`, 1), "", 409},
		{"unknown", "POST", url + "/mutations", `{"unexpected":true}`, "", 400},
		{"extra-json", "POST", url + "/mutations", body + ` {}`, "", 400},
		{"invalid-command", "POST", url + "/mutations", `{}`, "", 400},
		{"too-large", "POST", url + "/mutations", `{"reason":"` + strings.Repeat("a", 33000) + `"}`, "", 400},
		{"create-overwrite", "POST", "/api/memories", `{"id":"` + m.ID + `","kind":"fact","content":"overwrite"}`, "", 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := call(tc.method, tc.path, tc.body, tc.workspace)
			if w.Code != tc.status {
				t.Fatalf("status %d body %s", w.Code, w.Body)
			}
		})
	}
	w = call("POST", url+"/mutations", `{"operation_id":"delete","expected_version":2,"action":"delete","actor":"user","reason":"withdraw"}`, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deleted_at"`) {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	w = call("GET", url, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), `"content":"new"`) || !strings.Contains(w.Body.String(), `"previous_version":2`) {
		t.Fatalf("audit %d %s", w.Code, w.Body)
	}
}

func TestMemoryMutationHTTPFailureMapping(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{store.ErrMemoryMissing, 404}, {store.ErrMemoryConflict, 409}, {store.ErrMemoryMutationInvalid, 400},
		{memorypkg.EmbeddingError{Err: errors.New("offline")}, 502}, {errors.New("disk unavailable"), 500},
	} {
		w := httptest.NewRecorder()
		writeMemoryMutationFailure(w, httptest.NewRequest("POST", "/", nil), tc.err)
		if w.Code != tc.status {
			t.Fatalf("%v status %d", tc.err, w.Code)
		}
	}
	h := &Handler{memories: memoryFailureOperations{err: errors.New("offline")}}
	for _, handle := range []http.HandlerFunc{h.getMemory, h.mutateMemory} {
		w := httptest.NewRecorder()
		handle(w, httptest.NewRequest("POST", "/", nil))
		if w.Code != 503 {
			t.Fatalf("unsupported provider status %d", w.Code)
		}
	}
}
