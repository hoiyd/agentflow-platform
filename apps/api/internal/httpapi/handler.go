package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/runtimeinvariant"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
	"agentflow-platform/apps/api/internal/verification"
)

type MemoryCurationQueue interface {
	Enqueue(memorypkg.CurationJob) error
}

// KnowledgeOperations is the transport-facing subset of knowledge-base behavior.
type KnowledgeOperations interface {
	Ingest(context.Context, domain.DocumentIngestRequest) (domain.Document, error)
	Search(context.Context, domain.DocumentSearch, int) (domain.DocumentSearchResponse, error)
}

// HTTPStore deliberately exposes only global Agent configuration and a
// mandatory Workspace-scoped view for user-owned persistence.
type HTTPStore interface {
	store.AgentStore
	store.WorkspaceStoreProvider
}

// AgentRuntimeOperations is the transport-facing execution capability. HTTP
// handlers do not depend on Runtime construction or its internal collaborators.
type AgentRuntimeOperations interface {
	PrepareChatRunWithContract(context.Context, string, string, *domain.CompletionContract) (agent.PreparedRun, error)
	PrepareCollaborationRunWithContract(context.Context, string, string, *domain.CompletionContract) (agent.PreparedCollaborationRun, error)
	PrepareAutonomousRunWithContract(context.Context, string, string, *domain.CompletionContract) (agent.PreparedCollaborationRun, error)
	StreamChat(context.Context, agent.PreparedRun, []domain.Message, string) (<-chan domain.RunEvent, <-chan error)
	RunCollaboration(context.Context, agent.PreparedCollaborationRun, string) (<-chan domain.RunEvent, <-chan error)
	ContinueCollaboration(context.Context, string, string) (<-chan domain.RunEvent, <-chan error)
	RunAutonomous(context.Context, agent.PreparedCollaborationRun, string) (<-chan domain.RunEvent, <-chan error)
	ResumeAutonomous(context.Context, string, string) (<-chan domain.RunEvent, <-chan error)
	ResumeRecoverableAutonomous(context.Context, string, string) (<-chan domain.RunEvent, <-chan error)
	ResumeRecoverableCollaboration(context.Context, string) (<-chan domain.RunEvent, <-chan error)
	CompleteRun(string) (domain.Run, error)
	FailRun(string, error) (domain.Run, error)
	RejectRunCompletion(string, domain.RunStatus, string) (domain.Run, error)
	CancelRun(string) (domain.Run, error)
}

type ToolOperations interface {
	Catalog() (*tools.Catalog, error)
	List() ([]tools.ToolInfo, error)
	SetEnabled(string, bool) ([]tools.ToolInfo, error)
}

type RunCapacity interface {
	Reserve() (*concurrency.Reservation, error)
}

type VerificationOperations interface {
	FreezeContract(*domain.CompletionContract) (*domain.CompletionContract, error)
	Verify(context.Context, string, verification.Subject) (verification.Decision, error)
}

// Dependencies is the complete production dependency set for the HTTP adapter.
// Construction and lifecycle ownership remain in the app composition root.
type Dependencies struct {
	Store                HTTPStore
	ModelClient          modelprovider.TextCompleter
	Tools                ToolOperations
	AgentRuntime         AgentRuntimeOperations
	Knowledge            KnowledgeOperations
	MemoryCuration       MemoryCurationQueue
	RunController        RunCapacity
	Verification         VerificationOperations
	RuntimeInvariantMode string
	AllowedOrigins       []string
}

type Handler struct {
	store                HTTPStore
	modelClient          modelprovider.TextCompleter
	tools                ToolOperations
	agentRuntime         AgentRuntimeOperations
	knowledge            KnowledgeOperations
	memoryCuration       MemoryCurationQueue
	runController        RunCapacity
	verification         VerificationOperations
	runtimeInvariantMode runtimeinvariant.Mode
	allowedOrigins       []string
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
		store:                dependencies.Store,
		modelClient:          dependencies.ModelClient,
		tools:                dependencies.Tools,
		agentRuntime:         dependencies.AgentRuntime,
		knowledge:            dependencies.Knowledge,
		memoryCuration:       dependencies.MemoryCuration,
		runController:        dependencies.RunController,
		verification:         dependencies.Verification,
		runtimeInvariantMode: runtimeinvariant.NormalizeMode(dependencies.RuntimeInvariantMode),
		allowedOrigins:       append([]string(nil), dependencies.AllowedOrigins...),
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

func writeFailure(w http.ResponseWriter, r *http.Request, status int, err error) {
	info := describeHTTPFailure(status, err)
	requestID := ensureRequestID(w)
	logHTTPFailure(r, requestID, "json", status, err, info)
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

func failureChatChunk(w http.ResponseWriter, r *http.Request, status int, err error) domain.ChatChunk {
	info := describeHTTPFailure(status, err)
	retryable := info.Retryable
	requestID := ensureRequestID(w)
	logHTTPFailure(r, requestID, "sse", status, err, info)
	return domain.ChatChunk{
		Type: "error", Error: publicFailureMessage(status, err), ErrorCode: info.Code,
		ErrorSource: info.Source, ErrorCategory: string(info.Category), Retryable: &retryable, RequestID: requestID,
	}
}

func writeAPIError(w http.ResponseWriter, status int, message string, info failure.Info) {
	requestID := ensureRequestID(w)
	writeJSON(w, status, apiErrorResponse{
		Error: message, Code: info.Code, Source: info.Source,
		Category: string(info.Category), Retryable: info.Retryable, Operation: info.Operation, RequestID: requestID,
	})
}

func ensureRequestID(w http.ResponseWriter) string {
	requestID := w.Header().Get(RequestIDHeader)
	if requestID == "" {
		requestID = newRequestID()
		w.Header().Set(RequestIDHeader, requestID)
	}
	return requestID
}

func logHTTPFailure(r *http.Request, requestID string, transport string, status int, err error, info failure.Info) {
	log.Print(formatHTTPFailureLog(r, requestID, transport, status, err, info))
}

func formatHTTPFailureLog(r *http.Request, requestID string, transport string, status int, err error, info failure.Info) string {
	method, path := "", ""
	if r != nil {
		method = r.Method
		path = r.URL.Path
	}
	errorMessage := ""
	if err != nil {
		errorMessage = err.Error()
	}
	return fmt.Sprintf(
		"http_failure request_id=%q transport=%q method=%q path=%q status=%d code=%q source=%q category=%q retryable=%t operation=%q error=%q",
		requestID, transport, method, path, status, info.Code, info.Source, info.Category, info.Retryable, info.Operation, errorMessage,
	)
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
