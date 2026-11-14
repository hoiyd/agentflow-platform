package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"agentflow-platform/apps/api/internal/skill"
)

// Controls touch synthetic files only, behind the existing opt-in browser server.
func browserSkillReviews(roots, manifest string) error {
	catalog, err := skill.LoadRoots([]string{roots})
	if err != nil {
		return err
	}
	approvals := []map[string]string{}
	for _, item := range catalog.List() {
		approvals = append(approvals, map[string]string{"name": item.Name, "hash": item.Hash})
	}
	data, _ := json.Marshal(map[string]any{"schema_version": "skill-reviews-v1", "approvals": approvals})
	return os.WriteFile(manifest, data, 0600)
}

func browserSkillReviewControl(roots, manifest string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Action string `json:"action"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&input) != nil {
			w.WriteHeader(400)
			return
		}
		var err error
		switch input.Action {
		case "change":
			file := filepath.Join(roots, "auto-review", "SKILL.md")
			var data []byte
			data, err = os.ReadFile(file)
			if err == nil {
				err = os.WriteFile(file, append(data, []byte("\nUpdated reviewed method.\n")...), 0600)
			}
		case "approve":
			err = browserSkillReviews(roots, manifest)
		case "revoke":
			err = os.WriteFile(manifest, []byte(`{"schema_version":"skill-reviews-v1","approvals":[]}`), 0600)
		default:
			w.WriteHeader(400)
			return
		}
		if err != nil {
			http.Error(w, "review fixture unavailable", 500)
			return
		}
		w.WriteHeader(204)
	}
}
