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
	"agentflow-platform/apps/api/internal/failure"
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

// HTTPStore deliberately exposes only global Agent configuration and a
// mandatory Workspace-scoped view for user-owned persistence.
type HTTPStore interface {
	store.AgentStore
	store.WorkspaceStoreProvider
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
	store          HTTPStore
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
	return h.withCORS(withRequestID(h.withWorkspace(mux)))
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Workspace-ID")
		w.Header().Set("Access-Control-Expose-Headers", "Retry-After, X-Request-ID")
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
	writeAPIError(w, status, message, failureInfoForHTTPStatus(status))
}

type apiErrorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Source    string `json:"source"`
	Category  string `json:"category"`
	Retryable bool   `json:"retryable"`
	Operation string `json:"operation,omitempty"`
	RequestID string `json:"request_id"`
}

func writeFailure(w http.ResponseWriter, status int, err error) {
	info := describeHTTPFailure(status, err)
	writeAPIError(w, status, publicFailureMessage(status, err), info)
}

func describeHTTPFailure(status int, err error) failure.Info {
	info := failure.Describe(err)
	if info.Code == failure.CodeUnclassified {
		fallback := failureInfoForHTTPStatus(status)
		info.Code = fallback.Code
		info.Category = fallback.Category
		info.Retryable = fallback.Retryable
	}
	return info
}

func publicFailureMessage(status int, err error) string {
	message := http.StatusText(status)
	if status < http.StatusInternalServerError && err != nil {
		message = err.Error()
	}
	return message
}

func failureChatChunk(w http.ResponseWriter, status int, err error) domain.ChatChunk {
	info := describeHTTPFailure(status, err)
	retryable := info.Retryable
	requestID := w.Header().Get(RequestIDHeader)
	if requestID == "" {
		requestID = newRequestID()
		w.Header().Set(RequestIDHeader, requestID)
	}
	return domain.ChatChunk{
		Type: "error", Error: publicFailureMessage(status, err), ErrorCode: info.Code,
		ErrorSource: info.Source, ErrorCategory: string(info.Category), Retryable: &retryable, RequestID: requestID,
	}
}

func writeAPIError(w http.ResponseWriter, status int, message string, info failure.Info) {
	requestID := w.Header().Get(RequestIDHeader)
	if requestID == "" {
		requestID = newRequestID()
		w.Header().Set(RequestIDHeader, requestID)
	}
	writeJSON(w, status, apiErrorResponse{
		Error: message, Code: info.Code, Source: info.Source,
		Category: string(info.Category), Retryable: info.Retryable, Operation: info.Operation, RequestID: requestID,
	})
}

func failureInfoForHTTPStatus(status int) failure.Info {
	info := failure.Info{Code: "request_failed", Source: "http_api", Category: failure.CategoryExecution}
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		info.Code, info.Category = "invalid_request", failure.CategoryValidation
	case http.StatusUnauthorized:
		info.Code, info.Category = "unauthenticated", failure.CategoryAuthentication
	case http.StatusForbidden:
		info.Code, info.Category = "forbidden", failure.CategoryAuthentication
	case http.StatusNotFound:
		info.Code, info.Category = "not_found", failure.CategoryNotFound
	case http.StatusConflict:
		info.Code = "conflict"
	case http.StatusTooManyRequests:
		info.Code, info.Category, info.Retryable = "capacity_exceeded", failure.CategoryCapacity, true
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		info.Code, info.Category, info.Retryable = "service_unavailable", failure.CategoryAvailability, true
	case http.StatusGatewayTimeout:
		info.Code, info.Category, info.Retryable = failure.CodeTimeout, failure.CategoryTimeout, true
	default:
		if status >= http.StatusInternalServerError {
			info.Code, info.Category = "internal_error", failure.CategoryInternal
		}
	}
	return info
}
