package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

type Handler struct {
	store          store.Store
	openAI         *openai.Client
	tools          *tools.Manager
	agentRuntime   *agent.Runtime
	memorySyncer   *memorypkg.Syncer
	runController  *concurrency.RunController
	allowedOrigins []string
}

func NewHandler(store store.Store, openAI *openai.Client, tools *tools.Manager, allowedOrigins []string) *Handler {
	return NewHandlerWithRouterMode(store, openAI, tools, allowedOrigins, agent.RouterModeAuto)
}

func NewHandlerWithRouterMode(store store.Store, openAI *openai.Client, tools *tools.Manager, allowedOrigins []string, routerMode string) *Handler {
	return NewHandlerWithRouterModeAndLimits(store, openAI, tools, allowedOrigins, routerMode, agent.DefaultAutonomousLimits())
}

func NewHandlerWithRouterModeAndLimits(store store.Store, openAI *openai.Client, tools *tools.Manager, allowedOrigins []string, routerMode string, limits agent.AutonomousLimits) *Handler {
	return &Handler{
		store:        store,
		openAI:       openAI,
		tools:        tools,
		agentRuntime: agent.NewRuntimeWithRouterModeAndLimits(store, openAI, tools, routerMode, limits),
		memorySyncer: memorypkg.NewSyncer(store, openAI),
		runController: concurrency.NewRunController(concurrency.RunOptions{
			MaxConcurrent: concurrency.DefaultMaxConcurrentRuns,
			QueueSize:     concurrency.DefaultRunQueueSize,
			WaitTimeout:   concurrency.DefaultRunQueueWait,
		}),
		allowedOrigins: allowedOrigins,
	}
}

func (h *Handler) SetRunController(controller *concurrency.RunController) {
	if controller != nil {
		h.runController = controller
	}
}

func (h *Handler) Close(ctx context.Context) error {
	if h.memorySyncer == nil {
		return nil
	}
	return h.memorySyncer.Close(ctx)
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
