package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Mode is frozen read metadata, not an inference from Stage rows or an excuse
// to expose the Snapshot. No Stage is needed for any execution mode.
func TestRunReadsExposeFrozenModeWithoutSnapshot(t *testing.T) {
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		t.Run(mode, func(t *testing.T) {
			storage := fixturestore.New()
			conversation, err := storage.CreateConversation("frozen mode")
			if err != nil {
				t.Fatal(err)
			}
			snapshot := testRuntimeSnapshot()
			snapshot.Mode = mode
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			h := &Handler{store: storage}
			for _, endpoint := range []struct {
				path    string
				handler http.HandlerFunc
			}{
				{"/api/runs", h.listRuns},
				{"/api/runs/" + run.ID, h.getRun},
				{"/api/runs/" + run.ID + "/replay", h.getRunReplay},
			} {
				w := httptest.NewRecorder()
				endpoint.handler(w, httptest.NewRequest(http.MethodGet, endpoint.path, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: %d %s", endpoint.path, w.Code, w.Body.String())
				}
				var raw json.RawMessage = w.Body.Bytes()
				if endpoint.path == "/api/runs" {
					var list []json.RawMessage
					if err := json.Unmarshal(raw, &list); err != nil || len(list) != 1 {
						t.Fatalf("list: %s %v", raw, err)
					}
					raw = list[0]
				} else if endpoint.path == "/api/runs/"+run.ID+"/replay" {
					var replay map[string]json.RawMessage
					if err := json.Unmarshal(raw, &replay); err != nil {
						t.Fatal(err)
					}
					raw = replay["run"]
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				if string(fields["mode"]) != `"`+mode+`"` || fields["runtime_snapshot"] != nil {
					t.Fatalf("%s: incorrect public metadata: %s", endpoint.path, raw)
				}
			}
			stored, ok, err := storage.GetRun(run.ID)
			if err != nil || !ok || stored.RuntimeSnapshot == nil || stored.RuntimeSnapshot.Mode != mode {
				t.Fatalf("read mutated Snapshot: %+v %v", stored, err)
			}
		})
	}
}

func TestRunReadDoesNotInventLegacyMode(t *testing.T) {
	run := domain.Run{ID: "legacy"}
	hideRunSnapshot(&run)
	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["mode"]; ok {
		t.Fatalf("invented legacy mode: %s", data)
	}
}
