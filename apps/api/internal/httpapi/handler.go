package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

type Handler struct {
	store          store.Store
	openAI         *openai.Client
	tools          *tools.Manager
	agentRuntime   *agent.Runtime
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
		store:          store,
		openAI:         openAI,
		tools:          tools,
		agentRuntime:   agent.NewRuntimeWithRouterModeAndLimits(store, openAI, tools, routerMode, limits),
		allowedOrigins: allowedOrigins,
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.route)
	return h.withCORS(mux)
}

func (h *Handler) route(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case r.Method == http.MethodGet && path == "/health":
		h.health(w, r)
	case r.Method == http.MethodGet && path == "/api/conversations":
		h.listConversations(w, r)
	case r.Method == http.MethodPost && path == "/api/conversations":
		h.createConversation(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/api/conversations/") && !strings.HasSuffix(path, "/messages"):
		h.deleteConversation(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/conversations/") && strings.HasSuffix(path, "/messages"):
		h.listMessages(w, r)
	case r.Method == http.MethodPost && path == "/api/chat":
		h.chat(w, r)
	case r.Method == http.MethodGet && path == "/api/agents":
		h.listAgents(w, r)
	case r.Method == http.MethodPost && path == "/api/agents":
		h.createAgent(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/agents/"):
		h.getAgent(w, r)
	case r.Method == http.MethodGet && path == "/api/runs":
		h.listRuns(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/continue"):
		h.continueRun(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/resume"):
		h.resumeRun(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/cancel"):
		h.cancelRun(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/replay"):
		h.getRunReplay(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/runs/") && strings.HasSuffix(path, "/collaboration_steps"):
		h.listCollaborationSteps(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/api/runs/"):
		h.getRun(w, r)
	case r.Method == http.MethodGet && path == "/api/tools":
		h.listTools(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/tools/") && strings.HasSuffix(path, "/enable"):
		h.setToolEnabled(w, r, true)
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/tools/") && strings.HasSuffix(path, "/disable"):
		h.setToolEnabled(w, r, false)
	default:
		writeError(w, http.StatusNotFound, "route not found")
	}
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
