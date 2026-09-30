package projection

import (
	"encoding/json"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestSkillEvidenceRequiresActualRequestAndFrozenIdentity(t *testing.T) {
	snapshot := &domain.RuntimeSnapshot{Agent: domain.RuntimeAgentSnapshot{ID: "agent", Skills: []string{"method"}}, CandidateAgents: []domain.RuntimeAgentSnapshot{{ID: "other", Skills: []string{"method"}}}, Skills: []domain.SkillSnapshot{{Name: "method", Hash: "package", Resources: []domain.SkillResource{{Path: "references/check.md", Hash: "resource"}}}}}
	manifest := domain.ContextManifest{ID: "manifest", RunID: "run", StageID: "stage", TurnID: "turn", ModelCallID: "call", Entries: []domain.ContextManifestEntry{{Source: "skill_instructions", ReferenceID: "skill:method@package", Selected: true, EstimatedTokens: 12}}}
	raw, _ := json.Marshal(manifest)
	var payload any
	_ = json.Unmarshal(raw, &payload)
	events := []domain.RunEvent{
		{ID: "stage", Sequence: 1, Type: domain.EventStageStarted, StageID: "stage", Payload: map[string]any{"agent_id": "agent"}},
		{ID: "load", Sequence: 2, Type: domain.EventToolCompleted, StageID: "stage", TurnID: "turn", Payload: map[string]any{"tool_name": "skill_load", "result": map[string]any{"name": "method", "hash": "package", "agent_id": "agent"}}},
		{ID: "input", Sequence: 3, Type: domain.EventContextAssembled, StageID: "stage", TurnID: "turn", Payload: map[string]any{"manifest": payload}},
		{ID: "read", Sequence: 4, Type: domain.EventToolCompleted, StageID: "stage", TurnID: "turn", Payload: map[string]any{"tool_name": "skill_read", "result": map[string]any{"name": "method", "path": "references/check.md", "hash": "resource", "offset": 0, "next_offset": 8, "total_bytes": 8}}},
		{ID: "fail", Sequence: 5, Type: domain.EventToolFailed, StageID: "stage", TurnID: "turn", Payload: map[string]any{"tool_name": "skill_read", "arguments": `{"name":"method","path":"references/missing.md"}`, "error_code": "execution_failed"}},
	}
	records := []domain.ModelRequestRecord{{Envelope: domain.ModelRequestEnvelope{ID: "request", RunID: "run", StageID: "stage", TurnID: "turn", ModelCallID: "call", ContextManifestID: "manifest"}}}
	replay := domain.RunReplay{Run: domain.Run{ID: "run"}, RuntimeSnapshot: snapshot, RunEvents: events}
	rows := BuildSkillEvidence(replay, records)
	if len(rows) != 2 || rows[0].Instructions != "included" || rows[0].Activation != "model" || rows[0].FirstSequence != 3 || rows[0].RequestID != "request" || len(rows[0].Resources) != 1 || len(rows[0].Failures) != 1 || rows[1].Instructions != "not_observed" {
		t.Fatalf("evidence=%+v", rows)
	}
	replay.RunEvents = append(replay.RunEvents, domain.RunEvent{ID: "prepared", Sequence: 6, Type: domain.EventModelRequestPrepared, RunID: "run", StageID: "stage", TurnID: "turn", Payload: map[string]any{"record_id": "request"}})
	if rows := BuildSkillEvidence(replay, records); rows[0].FirstRequestSequence != 6 {
		t.Fatalf("missing request sequence: %+v", rows)
	}
	rows = BuildSkillEvidence(replay, records)
	if !reflect.DeepEqual(rows, BuildSkillEvidence(replay, records)) {
		t.Fatal("non deterministic evidence")
	}
	for _, test := range []struct {
		name   string
		mutate func(*domain.RunReplay, *[]domain.ModelRequestRecord)
	}{
		{"no request", func(_ *domain.RunReplay, r *[]domain.ModelRequestRecord) { *r = nil }},
		{"wrong run", func(_ *domain.RunReplay, r *[]domain.ModelRequestRecord) { (*r)[0].Envelope.RunID = "foreign" }},
		{"wrong scope", func(_ *domain.RunReplay, r *[]domain.ModelRequestRecord) { (*r)[0].Envelope.TurnID = "foreign" }},
		{"no manifest", func(r *domain.RunReplay, _ *[]domain.ModelRequestRecord) {
			r.RunEvents = append([]domain.RunEvent{}, events[:2]...)
		}},
		{"malformed manifest", func(r *domain.RunReplay, _ *[]domain.ModelRequestRecord) {
			r.RunEvents[2].Payload["manifest"] = "bad shape"
		}},
		{"foreign manifest", func(r *domain.RunReplay, _ *[]domain.ModelRequestRecord) {
			r.RunEvents[2].Payload["manifest"].(map[string]any)["run_id"] = "foreign"
		}},
		{"wrong Agent", func(r *domain.RunReplay, _ *[]domain.ModelRequestRecord) {
			r.RunEvents[2].Payload["manifest"].(map[string]any)["agent_id"] = "other"
		}},
		{"unselected", func(r *domain.RunReplay, _ *[]domain.ModelRequestRecord) {
			r.RunEvents[2].Payload["manifest"].(map[string]any)["entries"].([]any)[0].(map[string]any)["selected"] = false
		}},
		{"wrong package hash", func(r *domain.RunReplay, _ *[]domain.ModelRequestRecord) {
			r.RunEvents[2].Payload["manifest"].(map[string]any)["entries"].([]any)[0].(map[string]any)["reference_id"] = "skill:method@changed"
		}},
		{"metadata only", func(r *domain.RunReplay, _ *[]domain.ModelRequestRecord) {
			r.RunEvents[2].Payload["manifest"].(map[string]any)["entries"].([]any)[0].(map[string]any)["source"] = "skill_metadata"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, _ := json.Marshal(replay)
			var r domain.RunReplay
			_ = json.Unmarshal(data, &r)
			requests := append([]domain.ModelRequestRecord{}, records...)
			test.mutate(&r, &requests)
			for _, row := range BuildSkillEvidence(r, requests) {
				if row.Instructions != "not_observed" {
					t.Fatalf("invented inclusion: %+v", row)
				}
			}
		})
	}
	replay.RunEvents[3].Payload["truncated"] = true
	if len(BuildSkillEvidence(replay, records)[0].Resources) != 0 {
		t.Fatal("truncated page claimed as read")
	}
	delete(replay.RunEvents[3].Payload, "truncated")
	for _, result := range []map[string]any{
		{"name": "method", "path": "references/check.md", "hash": "resource"},
		{"name": "method", "path": "references/check.md", "hash": "resource", "offset": -1, "next_offset": 8, "total_bytes": 8},
		{"name": "method", "path": "references/check.md", "hash": "resource", "offset": 9, "next_offset": 8, "total_bytes": 8},
		{"name": "method", "path": "references/check.md", "hash": "resource", "offset": 0, "next_offset": 9, "total_bytes": 8},
	} {
		data, _ := json.Marshal(replay)
		var incomplete domain.RunReplay
		_ = json.Unmarshal(data, &incomplete)
		incomplete.RunEvents[3].Payload["result"] = result
		if len(BuildSkillEvidence(incomplete, records)[0].Resources) != 0 {
			t.Fatal("invented resource range", result)
		}
	}
	replay.RuntimeSnapshot = nil
	if len(BuildSkillEvidence(replay, records)) != 0 {
		t.Fatal("old Run gained frozen bindings")
	}
}

func TestSkillEvidenceExplicitRetentionAndScopeChanges(t *testing.T) {
	snapshot := &domain.RuntimeSnapshot{Agent: domain.RuntimeAgentSnapshot{ID: "agent", Skills: []string{"method"}}, CandidateAgents: []domain.RuntimeAgentSnapshot{{ID: "other", Skills: []string{"method"}}}, Skills: []domain.SkillSnapshot{{Name: "method", Hash: "hash"}}}
	replay := domain.RunReplay{Run: domain.Run{ID: "run"}, RuntimeSnapshot: snapshot}
	records := []domain.ModelRequestRecord{}
	for index, scope := range []struct{ agent, stage, activation string }{{"agent", "one", "explicit"}, {"agent", "two", ""}, {"other", "three", ""}} {
		id := scope.stage
		manifest := domain.ContextManifest{ID: id, RunID: "run", AgentID: scope.agent, StageID: scope.stage, TurnID: id, ModelCallID: id, Entries: []domain.ContextManifestEntry{{Source: "skill_instructions", ReferenceID: "skill:method@hash", Selected: true, Activation: scope.activation}}}
		replay.RunEvents = append(replay.RunEvents, domain.RunEvent{ID: id, Type: domain.EventContextAssembled, Sequence: int64(index + 1), Payload: map[string]any{"manifest": manifest}})
		records = append(records, domain.ModelRequestRecord{Envelope: domain.ModelRequestEnvelope{ID: id, RunID: "run", StageID: id, TurnID: id, ModelCallID: id, ContextManifestID: id}})
	}
	rows := BuildSkillEvidence(replay, records)
	if len(rows) != 3 || rows[0].Activation != "explicit" || rows[1].Activation != "explicit" || rows[2].Activation != "not_observed" {
		t.Fatalf("scope attribution=%+v", rows)
	}
	// Corrupt, failed, or unbound Tool evidence must never grant an activation.
	replay.RunEvents = append(replay.RunEvents, domain.RunEvent{Type: domain.EventTurnStarted, Sequence: 9, TurnID: "three", StageID: "three", Payload: map[string]any{"agent_id": "other"}})
	for _, payload := range []map[string]any{
		{"tool_name": "skill_load", "arguments": `{"name":"method"}`, "result": "bad"},
		{"tool_name": "skill_load", "result": map[string]any{"name": "method", "hash": "changed", "agent_id": "other"}},
		{"tool_name": "skill_load", "result": map[string]any{"name": "unknown", "hash": "hash", "agent_id": "other"}},
		{"tool_name": "skill_load", "result": map[string]any{"name": "method", "hash": "hash", "agent_id": "unbound"}},
		{"tool_name": "skill_load", "result": map[string]any{"name": "method", "hash": "hash", "agent_id": "other", "already_loaded": true}},
		{"tool_name": "skill_read", "result": map[string]any{"name": "method", "hash": "changed", "path": "missing"}},
	} {
		replay.RunEvents = append(replay.RunEvents, domain.RunEvent{Type: domain.EventToolCompleted, Sequence: 10, TurnID: "three", StageID: "three", Payload: payload})
	}
	if rows := BuildSkillEvidence(replay, records); rows[len(rows)-1].Activation != "not_observed" {
		t.Fatalf("invalid activation=%+v", rows)
	}
}
