package contextassembly

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestProjectGuidanceManifestRequiredAndDeduplicated(t *testing.T) {
	body := "Use ALPHA_REPORT headings."
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	session := Session{Config: DefaultConfig(), ProjectGuidance: &domain.ProjectGuidanceSnapshot{
		Selection: domain.ProjectContext{Root: "alpha", Paths: []string{"reports/input.md"}},
		Entries:   []domain.ProjectGuidanceEntry{{Path: "AGENTS.md", Hash: hash, Content: body, Reason: "project_entry", MatchedPaths: []string{"reports/input.md"}}, {Path: "rules/code.md", Reason: "path_not_matched"}},
	}}
	input := Request{Messages: []Message{{Role: "system", Content: "Follow the user."}, {Role: "user", Content: "Summarize this file."}}}
	if ProjectGuidanceTokens(session.ProjectGuidance) <= EstimateTokens(body) {
		t.Fatal("route estimate omitted guidance framing and policy")
	}
	first, err := Assemble(WithSession(context.Background(), session), input)
	if err != nil || !ContainsPrivateData(first.Manifest) {
		t.Fatalf("guidance assembly=%+v %v", first, err)
	}
	count := 0
	for _, entry := range first.Manifest.Entries {
		if entry.Source == SourceProjectGuidance && entry.Selected {
			count++
			if entry.ContentHash != hash || entry.SourcePath != "alpha/AGENTS.md" || entry.Reason != "project_entry" {
				t.Fatal(entry)
			}
		}
	}
	if count != 1 || first.Messages[len(first.Messages)-1].Content != "Summarize this file." {
		t.Fatal("project guidance lost user precedence or source identity", first)
	}
	again, err := Assemble(WithSession(context.Background(), session), Request{Messages: first.Messages})
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, message := range again.Messages {
		joined += message.Content
	}
	if strings.Count(joined, body) != 1 {
		t.Fatal("guidance duplicated on a Tool continuation")
	}
	session.LoadSkills = func() ([]domain.SkillSnapshot, error) {
		return []domain.SkillSnapshot{{Name: "method", Hash: "skill-hash", Instructions: body}}, nil
	}
	duplicate, err := Assemble(WithSession(t.Context(), session), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range duplicate.Manifest.Entries {
		if entry.Source == SourceProjectGuidance && entry.ReferenceID != "" && entry.Selected {
			t.Fatal("same active Skill instructions duplicated as project guidance")
		}
	}
	session.LoadSkills = nil
	session.Config.ContextWindowTokens, session.Config.OutputReserveTokens, session.Config.SafetyMarginTokens = 200, 1, 1
	_, err = Assemble(WithSession(t.Context(), session), Request{Messages: []Message{{Role: "user", Content: strings.Repeat("required ", 300)}}})
	if err == nil {
		t.Fatal("required guidance bypassed Context capacity")
	}
}
