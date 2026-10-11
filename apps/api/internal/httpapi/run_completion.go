package httpapi

import "agentflow-platform/apps/api/app/runcompletion"

func (h *Handler) completionDependencies() runcompletion.Dependencies {
	return runcompletion.Dependencies{Runtime: h.agentRuntime, Verification: h.verification}
}
