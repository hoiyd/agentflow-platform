package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.store.ListAgents()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var agent domain.Agent
	if err := json.NewDecoder(r.Body).Decode(&agent); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
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
	writeJSON(w, http.StatusOK, agent)
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.store.ListRuns()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
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
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
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
	writeJSON(w, http.StatusOK, replay)
}
