package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
	"agentflow-platform/apps/api/internal/verification"
)

type MemoryCurationQueue interface {
	Enqueue(memorypkg.CurationJob) error
}

// MemoryOperations is the transport-facing subset of semantic memory behavior.
type MemoryOperations interface {
	Create(context.Context, domain.Memory) (domain.Memory, error)
	Search(context.Context, domain.MemorySearch) ([]domain.RetrievedMemory, error)
}

// KnowledgeOperations is the transport-facing subset of knowledge-base behavior.
type KnowledgeOperations interface {
	Ingest(context.Context, domain.DocumentIngestRequest) (domain.Document, error)
	Search(context.Context, domain.DocumentSearch, int) (domain.DocumentSearchResponse, error)
	Evaluate(context.Context, domain.RAGEvaluationRunRequest) (domain.RAGEvaluationRunResponse, error)
}

// Dependencies is the complete production dependency set for the HTTP adapter.
// Construction and lifecycle ownership remain in the app composition root.
type Dependencies struct {
	Store          store.Store
	ModelClient    *openai.Client
	Tools          *tools.Manager
	AgentRuntime   *agent.Runtime
	Memory         MemoryOperations
	Knowledge      KnowledgeOperations
	MemoryCuration MemoryCurationQueue
	RunController  *concurrency.RunController
	Verification   *verification.Engine
	AllowedOrigins []string
}

type Handler struct {
	store          store.Store
	openAI         *openai.Client
	tools          *tools.Manager
	agentRuntime   *agent.Runtime
	memories       MemoryOperations
	knowledge      KnowledgeOperations
	memoryCuration MemoryCurationQueue
	runController  *concurrency.RunController
	verification   *verification.Engine
	allowedOrigins []string
}

func NewHandler(dependencies Dependencies) (*Handler, error) {
	if dependencies.Store == nil {
		return nil, errors.New("http api store is required")
	}
	if dependencies.ModelClient == nil {
		return nil, errors.New("http api model client is required")
	}
	if dependencies.Tools == nil {
		return nil, errors.New("http api tools manager is required")
	}
	if dependencies.AgentRuntime == nil {
		return nil, errors.New("http api agent runtime is required")
	}
	if dependencies.Memory == nil {
		return nil, errors.New("http api memory operations are required")
	}
	if dependencies.Knowledge == nil {
		return nil, errors.New("http api knowledge operations are required")
	}
	if dependencies.MemoryCuration == nil {
		return nil, errors.New("http api memory curation queue is required")
	}
	if dependencies.RunController == nil {
		return nil, errors.New("http api run controller is required")
	}
	if dependencies.Verification == nil {
		return nil, errors.New("http api verification engine is required")
	}
	return &Handler{
		store:          dependencies.Store,
		openAI:         dependencies.ModelClient,
		tools:          dependencies.Tools,
		agentRuntime:   dependencies.AgentRuntime,
		memories:       dependencies.Memory,
		knowledge:      dependencies.Knowledge,
		memoryCuration: dependencies.MemoryCuration,
		runController:  dependencies.RunController,
		verification:   dependencies.Verification,
		allowedOrigins: append([]string(nil), dependencies.AllowedOrigins...),
	}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	h.registerRoutes(mux)
	return h.withCORS(mux)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if h.isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) isAllowedOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	for _, allowed := range h.allowedOrigins {
		if strings.EqualFold(origin, allowed) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
