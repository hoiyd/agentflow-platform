package tooleval

import (
	"slices"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Sensor failure inventory: a forged completion, stale read-back, missing
// committed receipt, truncated input, wrong method hash or unpaired experiment
// must never become accepted evidence. These tests don't substitute for the
// real Binding/Postgres gate in cmd/eval.
func TestFileSkillRequiresOrderedDurableOutcome(t *testing.T) {
	data := FileSkillDataset{Material: "current facts"}
	task := FileSkillTask{ExpectedFile: "report", ExpectedReply: "SAVED"}
	event := func(name string, result map[string]any) domain.RunEvent {
		return domain.RunEvent{Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": name, "result": result, "tool_call_id": "write-call"}}
	}
	source := event("file_read", map[string]any{"root": "source", "path": "brief.md", "content": data.Material, "content_hash": fileContentHash(data.Material)})
	write := event("file_write", map[string]any{"root": "output", "path": "report.md", "new_hash": fileContentHash("report")})
	readback := event("file_read", map[string]any{"root": "output", "path": "report.md", "content": "report", "content_hash": fileContentHash("report")})
	for _, name := range []string{"valid", "write_before_source", "read_before_write", "missing_receipt", "wrong_call_receipt", "truncated_source"} {
		t.Run(name, func(t *testing.T) {
			s := FileSkillSample{Sample: Sample{Status: "completed", Arm: "without_skill", Output: "SAVED"}, FileContent: "report", FileHash: fileContentHash("report")}
			s.Replay.RunEvents = []domain.RunEvent{source, write, readback}
			s.Replay.ToolEffects = []domain.ToolEffectSummary{{ToolName: "file_write", ToolCallID: "write-call", Status: domain.ToolEffectCommitted, HasResult: true}}
			switch name {
			case "write_before_source":
				s.Replay.RunEvents = []domain.RunEvent{write, source, readback}
			case "read_before_write":
				s.Replay.RunEvents = []domain.RunEvent{source, readback, write}
			case "missing_receipt":
				s.Replay.ToolEffects = nil
			case "wrong_call_receipt":
				s.Replay.ToolEffects[0].ToolCallID = "other"
			case "truncated_source":
				truncated := event("file_read", map[string]any{"root": "source", "path": "brief.md", "content": data.Material, "content_hash": fileContentHash(data.Material), "truncated": true})
				s.Replay.RunEvents[0] = truncated
			}
			scoreFileSkill(data, task, domain.SkillSnapshot{}, &s)
			if s.Verified != (name == "valid") {
				t.Fatalf("unexpected verification: %+v", s.Findings)
			}
		})
	}
}

func TestFileSkillGateEmptyUnpairedAndCountedOnce(t *testing.T) {
	r := FileSkillReport{}
	finishFileSkill(&r)
	if r.Gate.Passed {
		t.Fatal("empty suite passed")
	}
	r.Samples = []FileSkillSample{{Sample: Sample{TaskID: "task", Trial: 1, Arm: "with_skill", Status: "failed"}}}
	finishFileSkill(&r)
	if r.Gate.Passed || r.Gate.BlockingFailures != 1 || len(r.Comparisons) != 1 || r.Comparisons[0].OutcomeDelta != nil {
		t.Fatalf("gate double counted or compared invalid pair: %+v", r)
	}
	finishFileSkill(&r)
	if len(r.Comparisons) != 1 {
		t.Fatal("re-finalization duplicated comparison")
	}
}

func TestFileSkillBoundaryOmitsOnlyDeclaredVariable(t *testing.T) {
	method := domain.SkillSnapshot{Name: "method", Hash: "hash"}
	base := &domain.RuntimeSnapshot{Tools: []domain.RuntimeToolSnapshot{{Name: "file_read"}}, Agent: domain.RuntimeAgentSnapshot{Tools: []string{"file_read"}}}
	candidate := *base
	candidate.Skills = []domain.SkillSnapshot{method}
	candidate.Tools = append(slices.Clone(base.Tools), domain.RuntimeToolSnapshot{Name: "skill_load"}, domain.RuntimeToolSnapshot{Name: "skill_read"})
	candidate.Agent.Tools = []string{"file_read", "skill_load", "skill_read"}
	candidate.Agent.Skills = []string{"method"}
	if fileSkillBoundary(base, method, "without_skill") != fileSkillBoundary(&candidate, method, "with_skill") {
		t.Fatal("declared experiment variable invalidated comparison")
	}
	candidate.Agent.SystemPrompt = "changed"
	if fileSkillBoundary(base, method, "without_skill") == fileSkillBoundary(&candidate, method, "with_skill") {
		t.Fatal("prompt drift compared")
	}
	candidate.Skills[0].Hash = "stale"
	if fileSkillBoundary(&candidate, method, "with_skill") != "" || fileSkillBoundary(nil, method, "without_skill") != "" || fileSkillBoundary(&candidate, method, "without_skill") != "" {
		t.Fatal("invalid method identity accepted")
	}
}

func TestFileSkillSelectionRequiresRequestAndExactResource(t *testing.T) {
	method := domain.SkillSnapshot{Name: "method", Hash: "package", Resources: []domain.SkillResource{{Path: "references/checklist.md", Hash: "resource", Content: "method rules"}}}
	for _, name := range []string{"valid", "wrong_hash", "partial", "wrong_path", "no_request", "wrong_package", "missing_instructions"} {
		t.Run(name, func(t *testing.T) {
			s := FileSkillSample{Sample: Sample{Status: "completed", Arm: "with_skill", Output: "OK"}}
			s.Replay.RunEvents = []domain.RunEvent{{Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "skill_load"}}, {Type: domain.EventToolCompleted, Payload: map[string]any{"tool_name": "skill_read"}}}
			e := domain.SkillEvidence{Name: method.Name, Hash: method.Hash, Bound: true, Instructions: "included", RequestID: "request", ManifestID: "manifest", Resources: []domain.SkillResourceEvidence{{Path: "references/checklist.md", Hash: "resource", TotalBytes: 12, NextOffset: 12}}}
			switch name {
			case "wrong_hash":
				e.Resources[0].Hash = "other"
			case "partial":
				e.Resources[0].NextOffset = 1
			case "wrong_path":
				e.Resources[0].Path = "references/other.md"
			case "no_request":
				e.RequestID = ""
			case "wrong_package":
				e.Hash = "stale"
			case "missing_instructions":
				e.Instructions = "not_observed"
			}
			s.Replay.Projection.SkillEvidence = []domain.SkillEvidence{e}
			scoreFileSkill(FileSkillDataset{}, FileSkillTask{ExpectedReply: "OK", ExpectSkill: true}, method, &s)
			if s.SelectionVerified != (name == "valid") {
				t.Fatalf("invalid input evidence accepted: %s", name)
			}
		})
	}
}
