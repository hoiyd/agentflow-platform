package tooleval

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/rag"
)

type KnowledgeReport struct {
	evalreport.Identity
	SchemaVersion    string                       `json:"schema_version"`
	Model            string                       `json:"model"`
	DocumentID       string                       `json:"document_id"`
	Embedding        domain.DocumentIndexIdentity `json:"index_identity"`
	Trials           int                          `json:"trials"`
	MaxRequests      int                          `json:"max_model_requests"`
	MaxTokens        int                          `json:"max_total_tokens"`
	SampleCalls      int                          `json:"sample_max_model_calls"`
	SampleTokens     int                          `json:"sample_max_tokens"`
	SampleTimeoutMS  int64                        `json:"sample_timeout_ms"`
	SuiteTimeoutMS   int64                        `json:"suite_timeout_ms"`
	PhysicalRequests int                          `json:"physical_requests"`
	TotalTokens      int                          `json:"total_tokens"`
	Verified         int                          `json:"verified"`
	NotEvaluated     int                          `json:"not_evaluated"`
	CostPerSuccess   *float64                     `json:"cost_per_verified_success_micros"`
	Gate             evalreport.Gate              `json:"gate"`
	Samples          []KnowledgeSample            `json:"samples"`
	Limitations      []string                     `json:"limitations"`
}

type KnowledgeSample struct {
	TaskID           string                        `json:"task_id"`
	Trial            int                           `json:"trial"`
	Status           string                        `json:"status"`
	RunID            string                        `json:"run_id,omitempty"`
	Verified         bool                          `json:"verified"`
	Findings         []string                      `json:"findings,omitempty"`
	SearchEventID    string                        `json:"search_event_id,omitempty"`
	CitedSource      *domain.RAGCitation           `json:"cited_source,omitempty"`
	ToolSetHash      string                        `json:"tool_set_hash,omitempty"`
	ContextAssembly  *domain.ContextAssemblyConfig `json:"context_assembly,omitempty"`
	PhysicalRequests int                           `json:"physical_requests"`
	LatencyMS        int64                         `json:"latency_ms"`
	Usage            domain.RunUsageTotals         `json:"usage"`
	UsageEstimated   bool                          `json:"usage_estimated"`
}

func (r *KnowledgeReport) finish() {
	var totalCost int64
	costKnown := true
	r.Gate.Reasons = []string{}
	for _, s := range r.Samples {
		if s.Status == "not_evaluated" {
			r.NotEvaluated++
		}
		if s.Verified {
			r.Verified++
		} else {
			r.Gate.BlockingSamples++
			if s.Status != "not_evaluated" {
				r.Gate.BlockingFailures++
			}
			r.Gate.Reasons = append(r.Gate.Reasons, fmt.Sprintf("%s/trial-%d: %s", s.TaskID, s.Trial, strings.Join(s.Findings, ", ")))
		}
		costKnown = costKnown && s.Usage.CostUnknownEntries == 0 && s.Usage.OpenReservations == 0 &&
			(s.PhysicalRequests == 0 || s.Usage.TotalTokens > 0)
		totalCost += s.Usage.EstimatedCostMicros
	}
	if costKnown && r.Verified > 0 {
		cost := float64(totalCost) / float64(r.Verified)
		r.CostPerSuccess = &cost
	}
	r.Gate.Passed = len(r.Samples) > 0 && r.Gate.BlockingSamples == 0
}

func scoreKnowledge(task knowledgeTask, doc domain.Document, replay domain.RunReplay, sample *KnowledgeSample) {
	var reply domain.Message
	for _, m := range replay.Messages {
		if m.Role == "assistant" {
			reply = m
		}
	}
	if replay.Run.Status != domain.RunCompleted {
		sample.Findings = append(sample.Findings, "run_not_completed")
	}
	issued := map[string]string{}
	type readEvidence struct {
		source      domain.RAGCitation
		searchEvent string
	}
	reads := map[string]readEvidence{}
	for _, e := range replay.RunEvents {
		name, _ := e.Payload["tool_name"].(string)
		switch e.Type {
		case domain.EventToolStarted:
			if task.Outcome == "direct" || (name != domain.KnowledgeSearchToolName && (task.Outcome != "cite" || name != domain.KnowledgeReadToolName)) {
				sample.Findings = append(sample.Findings, "unexpected_tool")
			}
		case domain.EventToolFailed:
			sample.Findings = append(sample.Findings, "tool_failed")
		case domain.EventToolCompleted:
			if e.Payload["truncated"] == true || e.Payload["error"] != "" && e.Payload["error"] != nil {
				sample.Findings = append(sample.Findings, "tool_evidence_unavailable")
				continue
			}
			switch name {
			case domain.KnowledgeSearchToolName:
				var result domain.KnowledgeToolSearchResult
				raw, _ := json.Marshal(e.Payload["result"])
				if json.Unmarshal(raw, &result) != nil || !strings.Contains(strings.ToUpper(result.Query), task.Target) {
					continue
				}
				if task.Outcome == "abstain" && result.NoMatch && len(result.Items) == 0 {
					sample.SearchEventID = e.ID
				}
				for _, item := range result.Items {
					if task.Outcome == "cite" && !result.NoMatch && item.DocumentID == doc.ID && item.DocumentVersion == doc.Version && !strings.Contains(item.Preview, task.Evidence) {
						issued[item.Reference] = e.ID
					}
				}
			case domain.KnowledgeReadToolName:
				result, ok := rag.KnowledgeReadFromEvent(e)
				if ok && issued[result.Reference] != "" && result.Source.DocumentID == doc.ID && result.Source.DocumentVersion == doc.Version &&
					result.Reference == result.Source.ChunkID && strings.Contains(result.Content, task.Evidence) {
					reads[e.ID] = readEvidence{source: result.Source, searchEvent: issued[result.Reference]}
				}
			}
		}
	}
	want := task.Answer
	switch task.Outcome {
	case "cite":
		if len(reads) == 0 {
			sample.Findings = append(sample.Findings, "missing_ordered_read_evidence")
		}
		if len(reply.Citations) == 1 {
			citation := reply.Citations[0]
			read, ok := reads[citation.ToolEventID]
			src := read.source
			if ok && citation.SourceID == src.SourceID && citation.ChunkID == src.ChunkID && citation.DocumentID == src.DocumentID &&
				citation.DocumentVersion == src.DocumentVersion && citation.RunID == src.RunID && citation.ToolCallID == src.ToolCallID {
				sample.CitedSource = &citation
				sample.SearchEventID = read.searchEvent
				want = fmt.Sprintf(want, citation.SourceID)
			}
		}
		if sample.CitedSource == nil {
			sample.Findings = append(sample.Findings, "unsupported_citation")
		}
	case "abstain":
		if sample.SearchEventID == "" {
			sample.Findings = append(sample.Findings, "missing_no_match_evidence")
		}
	}
	if strings.TrimSpace(reply.Content) != want || len(reply.WebCitations) > 0 || task.Outcome != "cite" && len(reply.Citations) > 0 {
		sample.Findings = append(sample.Findings, "incorrect_final_reply")
	}
	sample.Verified = len(sample.Findings) == 0
}
