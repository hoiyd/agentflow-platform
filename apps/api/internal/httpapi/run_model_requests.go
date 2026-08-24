package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/requestcapture"
)

type modelRequestDebugRecord struct {
	Envelope   domain.ModelRequestEnvelope `json:"envelope"`
	Capture    domain.ModelRequestCapture  `json:"capture"`
	Manifest   *domain.ContextManifest     `json:"manifest,omitempty"`
	SourceDiff modelRequestSourceDiff      `json:"source_diff"`
}

type modelRequestSourceDiff struct {
	EnvelopeSelectedTokens map[string]int `json:"envelope_selected_tokens"`
	ManifestSelectedTokens map[string]int `json:"manifest_selected_tokens"`
	ManifestExcludedTokens map[string]int `json:"manifest_excluded_tokens"`
	MatchesEnvelope        bool           `json:"matches_envelope"`
}

type modelRequestDebugResponse struct {
	RunID                    string                    `json:"run_id"`
	ReconstructabilityStatus string                    `json:"reconstructability_status"`
	InvariantError           string                    `json:"invariant_error,omitempty"`
	Records                  []modelRequestDebugRecord `json:"records"`
}

func (h *Handler) listModelRequests(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/model_requests"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	scoped := h.scopedStore(r)
	run, ok, err := scoped.GetRun(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	records, err := scoped.ListModelRequestRecords(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	events, err := scoped.ListRunEvents(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	manifests := modelRequestManifests(events)
	includeContent := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("include_content")), "true")
	items := make([]modelRequestDebugRecord, 0, len(records))
	for _, record := range records {
		capture := record.Capture
		if !includeContent {
			capture.Content = ""
		}
		item := modelRequestDebugRecord{Envelope: record.Envelope, Capture: capture, SourceDiff: modelRequestSourceDiff{
			EnvelopeSelectedTokens: cloneIntMap(record.Envelope.SourceTokenBreakdown),
			ManifestSelectedTokens: map[string]int{}, ManifestExcludedTokens: map[string]int{},
		}}
		if manifest, exists := manifests[record.Envelope.ContextManifestID]; exists {
			copy := manifest
			item.Manifest = &copy
			item.SourceDiff.ManifestSelectedTokens, item.SourceDiff.ManifestExcludedTokens = manifestTokenBreakdown(manifest)
			item.SourceDiff.MatchesEnvelope = equalIntMap(item.SourceDiff.EnvelopeSelectedTokens, item.SourceDiff.ManifestSelectedTokens)
		}
		items = append(items, item)
	}
	response := modelRequestDebugResponse{RunID: id, ReconstructabilityStatus: "valid", Records: items}
	if err := requestcapture.ValidateReconstructability(run, records, events); err != nil {
		response.ReconstructabilityStatus = "invalid"
		response.InvariantError = err.Error()
	}
	writeJSON(w, http.StatusOK, response)
}

func manifestTokenBreakdown(manifest domain.ContextManifest) (map[string]int, map[string]int) {
	selected := map[string]int{}
	excluded := map[string]int{}
	for _, entry := range manifest.Entries {
		if entry.EstimatedTokens <= 0 {
			continue
		}
		if entry.Selected {
			selected[entry.Source] += entry.EstimatedTokens
		} else {
			excluded[entry.Source] += entry.EstimatedTokens
		}
	}
	return selected, excluded
}

func cloneIntMap(value map[string]int) map[string]int {
	result := make(map[string]int, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func equalIntMap(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func modelRequestManifests(events []domain.RunEvent) map[string]domain.ContextManifest {
	result := map[string]domain.ContextManifest{}
	for _, item := range events {
		if item.Type != domain.EventContextAssembled {
			continue
		}
		encoded, err := json.Marshal(item.Payload["manifest"])
		if err != nil {
			continue
		}
		var manifest domain.ContextManifest
		if json.Unmarshal(encoded, &manifest) == nil && manifest.ID != "" {
			result[manifest.ID] = manifest
		}
	}
	return result
}
