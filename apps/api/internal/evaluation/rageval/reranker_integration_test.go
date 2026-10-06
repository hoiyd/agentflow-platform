package rageval

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/rag"
)

func TestCrossEncoderEvaluationRequiresConsentAndRetainsFailures(t *testing.T) {
	dataset, manifest := writeFixture(t, "Frankfurt", false)
	baseline, err := Run(t.Context(), Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, rejected := range []bool{false, true} {
		t.Run(map[bool]string{false: "successful ablation", true: "service failure"}[rejected], func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if rejected {
					http.Error(w, "private provider body", 503)
					return
				}
				var body struct {
					Texts []string `json:"texts"`
				}
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid request")
					return
				}
				ranks := []map[string]any{}
				for index := range body.Texts {
					ranks = append(ranks, map[string]any{"index": index, "score": 0.9})
				}
				_ = json.NewEncoder(w).Encode(ranks)
			}))
			defer server.Close()
			opts := Options{DatasetPath: dataset, CorpusManifestPath: manifest, TopK: 1, Reranker: rag.RerankerConfig{Mode: "tei", BaseURL: server.URL, Model: "fixture-model", Revision: "fixture-revision"}}
			if _, err := Run(t.Context(), opts); err == nil || calls.Load() != 0 {
				t.Fatalf("unapproved network: calls=%d err=%v", calls.Load(), err)
			}
			opts.LiveReranking = true
			report, err := Run(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if report.Pipeline.Reranker.Provider != "tei" || !strings.Contains(report.CostSource, "unavailable") || calls.Load() == 0 {
				t.Fatalf("metadata/report=%+v calls=%d", report, calls.Load())
			}
			if rejected {
				if report.Summary.Failed == 0 || report.Gate.Passed || report.Samples[0].ErrorCode != "reranker_unavailable" || strings.Contains(report.Samples[0].Error, "private provider body") {
					t.Fatalf("failure not retained: %+v", report)
				}
			} else {
				if report.Samples[0].CandidateSetHash == "" || report.Samples[0].CandidateSetHash != baseline.Samples[0].CandidateSetHash {
					t.Fatal("reranker ablation did not retain identical candidate inputs")
				}
				ApplyComparison(&report, baseline, true)
				if report.Comparison == nil || !report.Comparison.Comparable || len(report.Comparison.ChangedVariables) != 1 || report.Comparison.ChangedVariables[0] != "reranker" {
					t.Fatalf("comparison=%+v", report.Comparison)
				}
				for _, hash := range []string{"", "different-candidates"} {
					changed := baseline
					changed.Samples = append([]Sample(nil), baseline.Samples...)
					changed.Samples[0].CandidateSetHash = hash
					comparison := Compare(report, changed, true)
					if comparison.Comparable || len(comparison.Deltas) != 0 {
						t.Fatalf("unproven candidate set allowed numeric deltas: %+v", comparison)
					}
				}
			}
		})
	}
}
