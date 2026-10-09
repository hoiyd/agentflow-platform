package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/apicontract"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/availability"
)

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.scopedStore(r).ListAgents()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

func (h *Handler) listSkills(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.skills.List())
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var req apicontract.AgentConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	agent := domain.Agent{
		WorkspaceID:      workspaceIDFromRequest(r),
		MemoryEnabled:    true,
		RetrievalEnabled: true,
		Executor:         domain.DefaultAgentExecutor,
	}
	applyAgentConfigRequest(&agent, req)
	if rejectCredentialContent(w, r, req) {
		return
	}
	if err := h.validateWorkspaceAgentTools(r, agent.Tools); err != nil {
		writeFailure(w, r, http.StatusBadRequest, err)
		return
	}
	if len(agent.Skills) > 0 {
		if err := h.agentRuntime.ValidateAgentSkills(agent); err != nil {
			writeFailure(w, r, http.StatusBadRequest, err)
			return
		}
	}

	created, err := h.scopedStore(r).CreateAgent(agent)
	if err != nil {
		writeFailure(w, r, http.StatusBadRequest, err)
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

	agent, ok, err := h.scopedStore(r).GetAgent(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
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
	existing, ok, err := h.scopedStore(r).GetAgent(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if existing.IsTemplate {
		writeError(w, http.StatusForbidden, "Built-in templates are read-only; create a Workspace copy")
		return
	}
	if err := h.scopedStore(r).ArchiveAgent(id); err != nil {
		status := http.StatusBadRequest
		if store.IsNotFound(err) {
			status = http.StatusNotFound
		}
		writeFailure(w, r, status, err)
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
	existing, ok, err := h.scopedStore(r).GetAgent(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
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
	var req apicontract.AgentConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	agent := domain.NormalizeAgentConfig(existing)
	if existing.IsTemplate {
		writeError(w, http.StatusForbidden, "Built-in templates are read-only; create a Workspace copy")
		return
	}
	applyAgentConfigRequest(&agent, req)
	if rejectCredentialContent(w, r, req) {
		return
	}
	if err := h.validateWorkspaceAgentTools(r, agent.Tools); err != nil {
		writeFailure(w, r, http.StatusBadRequest, err)
		return
	}
	if len(agent.Skills) > 0 {
		if err := h.agentRuntime.ValidateAgentSkills(agent); err != nil {
			writeFailure(w, r, http.StatusBadRequest, err)
			return
		}
	}
	updated, err := h.scopedStore(r).UpdateAgent(agent)
	if err != nil {
		writeFailure(w, r, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) validateWorkspaceAgentTools(r *http.Request, names []string) error {
	service, err := h.currentToolCatalog()
	if err != nil {
		return err
	}
	catalog, _, err := availability.Resolve(h.store, workspaceIDFromRequest(r), service)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := availability.RequireTool(catalog, strings.TrimSpace(name)); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) currentToolCatalog() (*tool.Catalog, error) {
	if h.tools != nil {
		return h.tools.Catalog()
	}
	return tool.DefaultCatalog(), nil
}

func applyAgentConfigRequest(agent *domain.Agent, req apicontract.AgentConfigRequest) {
	if req.Name != nil {
		agent.Name = *req.Name
	}
	if req.Description != nil {
		agent.Description = *req.Description
	}
	if req.SystemPrompt != nil {
		agent.SystemPrompt = *req.SystemPrompt
	}
	if req.RoutingHints != nil {
		agent.RoutingHints = domain.AgentRoutingHints{
			Capabilities: append([]string(nil), req.RoutingHints.Capabilities...),
			TaskExamples: append([]string(nil), req.RoutingHints.TaskExamples...),
			Exclusions:   append([]string(nil), req.RoutingHints.Exclusions...),
		}
	}
	if req.Tools != nil {
		agent.Tools = append([]string(nil), (*req.Tools)...)
	}
	if req.Skills != nil {
		agent.Skills = append([]string(nil), (*req.Skills)...)
	}
	if req.MemoryEnabled != nil {
		agent.MemoryEnabled = *req.MemoryEnabled
	}
	if req.RetrievalEnabled != nil {
		agent.RetrievalEnabled = *req.RetrievalEnabled
	}
	agent.Executor = domain.DefaultAgentExecutor
}

func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.scopedStore(r).ListRuns()
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	for index := range runs {
		run := &runs[index]
		run.RuntimeSnapshot = nil
	}
	writeJSON(w, http.StatusOK, runs)
}

func (h *Handler) getRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	run, ok, err := h.scopedStore(r).GetRun(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
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
	run, ok, err := h.scopedStore(r).GetRun(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	run, err = h.agentRuntime.CancelRun(id)
	if err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeFailure(w, r, http.StatusInternalServerError, err)
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

	scoped := h.scopedStore(r)
	if _, ok, err := scoped.GetRun(id); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	steps, err := scoped.ListCollaborationSteps(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
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
	scoped := h.scopedStore(r)
	if _, ok, err := scoped.GetRun(id); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	replay, ok, err := scoped.GetRunReplay(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if err := h.enrichRunProjection(scoped, &replay); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
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
	scoped := h.scopedStore(r)
	if _, ok, err := scoped.GetRun(id); err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	ledger, ok, err := scoped.GetRunUsageLedger(id)
	if err != nil {
		writeFailure(w, r, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, ledger)
}
