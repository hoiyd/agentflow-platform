package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	knowledgepkg "agentflow-platform/apps/api/internal/knowledge"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
	toolpkg "agentflow-platform/apps/api/internal/tools"
	"agentflow-platform/apps/api/internal/verification"
)

type memoryOperationsStub struct{}

func (*memoryOperationsStub) Commit(_ context.Context, memory domain.Memory) (domain.Memory, error) {
	return memory, nil
}
func (*memoryOperationsStub) Recall(context.Context, domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	return nil, nil
}
func (*memoryOperationsStub) SyncTurn(memorypkg.TurnSyncRequest) error { return nil }

type knowledgeOperationsStub struct{}

func (*knowledgeOperationsStub) Ingest(_ context.Context, request domain.DocumentIngestRequest) (domain.Document, error) {
	return domain.Document{Title: request.Title}, nil
}
func (*knowledgeOperationsStub) Search(context.Context, domain.DocumentSearch, int) (domain.DocumentSearchResponse, error) {
	return domain.DocumentSearchResponse{}, nil
}
func (*knowledgeOperationsStub) Evaluate(context.Context, domain.RAGEvaluationRunRequest) (domain.RAGEvaluationRunResponse, error) {
	return domain.RAGEvaluationRunResponse{}, nil
}

func TestNewHandlerValidatesEveryRequiredDependency(t *testing.T) {
	dependencies := completeHandlerDependencies(t)
	handler, err := NewHandler(dependencies)
	if err != nil || handler == nil || len(handler.allowedOrigins) != 1 {
		t.Fatalf("new handler: handler=%#v err=%v", handler, err)
	}

	tests := []struct {
		name string
		edit func(*Dependencies)
		want string
	}{
		{name: "store", edit: func(d *Dependencies) { d.Store = nil }, want: "http api store is required"},
		{name: "model client", edit: func(d *Dependencies) { d.ModelClient = nil }, want: "http api model client is required"},
		{name: "tools", edit: func(d *Dependencies) { d.Tools = nil }, want: "http api tools manager is required"},
		{name: "agent runtime", edit: func(d *Dependencies) { d.AgentRuntime = nil }, want: "http api agent runtime is required"},
		{name: "memory", edit: func(d *Dependencies) { d.Memory = nil }, want: "http api memory operations are required"},
		{name: "knowledge", edit: func(d *Dependencies) { d.Knowledge = nil }, want: "http api knowledge operations are required"},
		{name: "run controller", edit: func(d *Dependencies) { d.RunController = nil }, want: "http api run controller is required"},
		{name: "verification", edit: func(d *Dependencies) { d.Verification = nil }, want: "http api verification engine is required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := dependencies
			test.edit(&candidate)
			if _, err := NewHandler(candidate); err == nil || err.Error() != test.want {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestCORSAllowlistAndKnowledgeErrorMapping(t *testing.T) {
	handler := &Handler{allowedOrigins: []string{"https://app.example.com"}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })

	allowed := httptest.NewRecorder()
	allowedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	allowedRequest.Header.Set("Origin", "HTTPS://APP.EXAMPLE.COM")
	handler.withCORS(next).ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusAccepted || allowed.Header().Get("Access-Control-Allow-Origin") != "HTTPS://APP.EXAMPLE.COM" || allowed.Header().Get("Vary") != "Origin" {
		t.Fatalf("unexpected allowed CORS response: status=%d headers=%v", allowed.Code, allowed.Header())
	}

	denied := httptest.NewRecorder()
	deniedRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	deniedRequest.Header.Set("Origin", "https://other.example.com")
	handler.withCORS(next).ServeHTTP(denied, deniedRequest)
	if denied.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("unexpected denied CORS header: %v", denied.Header())
	}

	badRequest := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/documents", nil)
	writeKnowledgeError(badRequest, request, errors.New("invalid document"))
	if badRequest.Code != http.StatusBadRequest {
		t.Fatalf("expected knowledge validation status 400, got %d", badRequest.Code)
	}
	badGateway := httptest.NewRecorder()
	writeKnowledgeError(badGateway, request, knowledgepkg.EmbeddingError{Err: errors.New("embedding unavailable")})
	if badGateway.Code != http.StatusBadGateway {
		t.Fatalf("expected embedding status 502, got %d", badGateway.Code)
	}
}

func completeHandlerDependencies(t *testing.T) Dependencies {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	toolPath := filepath.Join(t.TempDir(), "tools.json")
	if err := toolpkg.SaveConfig(toolPath, toolpkg.DefaultConfig()); err != nil {
		t.Fatalf("save tools: %v", err)
	}
	manager, err := toolpkg.NewManager(toolPath)
	if err != nil {
		t.Fatalf("new tools manager: %v", err)
	}
	registry := verification.NewRegistry(verification.Options{})
	return Dependencies{
		Store: fileStore, ModelClient: client, Tools: manager,
		AgentRuntime: agentpkg.NewRuntime(agentpkg.RuntimeOptions{Store: fileStore, ModelClient: client}),
		Memory:       &memoryOperationsStub{},
		Knowledge:    &knowledgeOperationsStub{},
		RunController: concurrency.NewRunController(concurrency.RunOptions{
			MaxConcurrent: 1, QueueSize: 1, WaitTimeout: time.Second,
		}),
		Verification:   verification.NewEngine(fileStore, registry),
		AllowedOrigins: []string{"https://app.example.com"},
	}
}

func fullStoreForTest(t *testing.T, dependencies Dependencies) store.Store {
	t.Helper()
	fullStore, ok := dependencies.Store.(store.Store)
	if !ok {
		t.Fatal("test dependencies do not expose the full store fixture")
	}
	return fullStore
}
