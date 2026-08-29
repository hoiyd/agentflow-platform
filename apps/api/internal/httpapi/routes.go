package httpapi

import "net/http"

func (h *Handler) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /api/chat", h.chat)

	h.registerConversationRoutes(mux)
	h.registerDocumentRoutes(mux)
	h.registerAgentRoutes(mux)
	h.registerRunRoutes(mux)
	h.registerToolRoutes(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "route not found")
	})
}

func (h *Handler) registerConversationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/conversations", h.listConversations)
	mux.HandleFunc("POST /api/conversations", h.createConversation)
	mux.HandleFunc("PATCH /api/conversations/{id}", h.updateConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", h.deleteConversation)
	mux.HandleFunc("GET /api/conversations/{id}/messages", h.listMessages)
	mux.HandleFunc("GET /api/conversations/{id}/task-state", h.getTaskState)
	mux.HandleFunc("PATCH /api/conversations/{id}/task-state", h.patchTaskState)
	mux.HandleFunc("GET /api/conversations/{id}/task-state/revisions", h.listTaskStateRevisions)
	mux.HandleFunc("GET /api/conversations/{id}/task-state/revisions/{version}", h.getTaskStateRevision)
}

func (h *Handler) registerDocumentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/documents", h.listDocuments)
	mux.HandleFunc("POST /api/documents", h.createDocument)
	mux.HandleFunc("POST /api/documents/upload", h.uploadDocument)
	mux.HandleFunc("GET /api/documents/{id}", h.getDocument)
	mux.HandleFunc("DELETE /api/documents/{id}", h.deleteDocument)
	mux.HandleFunc("POST /api/rag/search", h.searchDocumentChunks)
}

func (h *Handler) registerAgentRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/agents", h.listAgents)
	mux.HandleFunc("POST /api/agents", h.createAgent)
	mux.HandleFunc("GET /api/agents/{id}", h.getAgent)
	mux.HandleFunc("PATCH /api/agents/{id}", h.updateAgent)
	mux.HandleFunc("DELETE /api/agents/{id}", h.archiveAgent)
}

func (h *Handler) registerRunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/runs", h.listRuns)
	mux.HandleFunc("GET /api/runs/{id}", h.getRun)
	mux.HandleFunc("POST /api/runs/{id}/continue", h.continueRun)
	mux.HandleFunc("POST /api/runs/{id}/resume", h.resumeRun)
	mux.HandleFunc("POST /api/runs/{id}/cancel", h.cancelRun)
	mux.HandleFunc("POST /api/runs/{id}/verify", h.verifyRun)
	mux.HandleFunc("GET /api/runs/{id}/replay", h.getRunReplay)
	mux.HandleFunc("GET /api/runs/{id}/projection", h.getRunProjection)
	mux.HandleFunc("GET /api/runs/{id}/usage", h.getRunUsage)
	mux.HandleFunc("GET /api/runs/{id}/model_requests", h.listModelRequests)
	mux.HandleFunc("GET /api/runs/{id}/episode", h.getEpisodeReport)
	mux.HandleFunc("GET /api/runs/{id}/collaboration_steps", h.listCollaborationSteps)
}

func (h *Handler) registerToolRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/tools", h.listTools)
	mux.HandleFunc("POST /api/tools/{name}/enable", func(w http.ResponseWriter, r *http.Request) {
		h.setToolEnabled(w, r, true)
	})
	mux.HandleFunc("POST /api/tools/{name}/disable", func(w http.ResponseWriter, r *http.Request) {
		h.setToolEnabled(w, r, false)
	})
}
