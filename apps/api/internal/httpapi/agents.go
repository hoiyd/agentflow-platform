package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

type agentConfigRequest struct {
	ID               string   `json:"id,omitempty"`
	Name             *string  `json:"name,omitempty"`
	Description      *string  `json:"description,omitempty"`
	SystemPrompt     *string  `json:"system_prompt,omitempty"`
	Tools            []string `json:"tools,omitempty"`
	MemoryEnabled    *bool    `json:"memory_enabled,omitempty"`
	RetrievalEnabled *bool    `json:"retrieval_enabled,omitempty"`
	Executor         *string  `json:"executor,omitempty"`
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListAgents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var req agentConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	agent := domain.Agent{
		ID:               strings.TrimSpace(req.ID),
		MemoryEnabled:    true,
		RetrievalEnabled: true,
		Executor:         domain.DefaultAgentExecutor,
	}
	applyAgentConfigRequest(&agent, req)
	if err := h.validateAgentTools(agent.Tools); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	created, err := h.store.CreateAgent(agent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/agents/"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "agent id is required")
		return
	}

	agent, ok, err := h.store.GetAgent(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if agent.Archived {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, agent)
}

func (h *Handler) archiveAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/agents/"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "agent id is required")
		return
	}
	if domain.IsDefaultAgentID(id) {
		writeError(w, http.StatusBadRequest, "default agents cannot be archived")
		return
	}
	if err := h.store.ArchiveAgent(id); err != nil {
		status := http.StatusBadRequest
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/agents/"))
	if id == "" || strings.Contains(id, "/") {
		writeError(w, http.StatusBadRequest, "agent id is required")
		return
	}
	existing, ok, err := h.store.GetAgent(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if existing.Archived {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	var req agentConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	agent := domain.NormalizeAgentConfig(existing)
	applyAgentConfigRequest(&agent, req)
	if err := h.validateAgentTools(agent.Tools); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := h.store.UpdateAgent(agent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) validateAgentTools(names []string) error {
	catalog, err := h.currentToolCatalog()
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, ok := catalog.Installed(strings.TrimSpace(name)); !ok {
			return fmt.Errorf("tool %q is not installed", name)
		}
	}
	return nil
}

func (h *Handler) currentToolCatalog() (*tools.Catalog, error) {
	if h.tools != nil {
		return h.tools.Catalog()
	}
	return tools.DefaultCatalog(), nil
}

func applyAgentConfigRequest(agent *domain.Agent, req agentConfigRequest) {
	if req.Name != nil {
		agent.Name = *req.Name
	}
	if req.Description != nil {
		agent.Description = *req.Description
	}
	if req.SystemPrompt != nil {
		agent.SystemPrompt = *req.SystemPrompt
	}
	if req.Tools != nil {
		agent.Tools = req.Tools
	}
	if req.MemoryEnabled != nil {
		agent.MemoryEnabled = *req.MemoryEnabled
	}
	if req.RetrievalEnabled != nil {
		agent.RetrievalEnabled = *req.RetrievalEnabled
	}
	if req.Executor != nil {
		agent.Executor = strings.TrimSpace(*req.Executor)
	}
	if strings.TrimSpace(agent.Executor) == "" {
		agent.Executor = domain.DefaultAgentExecutor
	}
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.store.ListRuns()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range runs {
		runs[i].RuntimeSnapshot = nil
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	run, ok, err := h.store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	run.RuntimeSnapshot = nil
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/cancel"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	run, err := h.agentRuntime.CancelRun(id)
	if err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	run.RuntimeSnapshot = nil
	writeJSON(w, http.StatusOK, run)
}

func (h *Handler) listCollaborationSteps(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/collaboration_steps"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	if _, ok, err := h.store.GetRun(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	steps, err := h.store.ListCollaborationSteps(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

func (h *Handler) getRunReplay(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/replay"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	replay, ok, err := h.store.GetRunReplay(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	replay.Run.RuntimeSnapshot = nil
	writeJSON(w, http.StatusOK, replay)
}

func (h *Handler) getRunUsage(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/usage"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	ledger, ok, err := h.store.GetRunUsageLedger(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, ledger)
}
