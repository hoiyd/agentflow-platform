package tokenization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

const corpusVersion = "tokenization-calibration-v1"

type corpusCase struct {
	id              string
	system          string
	user            string
	history         []domain.Message
	knowledge       []domain.RetrievedDocumentChunk
	withTool        bool
	rejectPreflight bool
}

func corpus(inputBudget int) []corpusCase {
	nearWords := max(1, inputBudget*3/len("boundary "))
	return []corpusCase{
		{id: "english", system: "Answer concisely.", user: "Explain what a token budget protects. Reply in one sentence."},
		{id: "chinese", system: "请简短回答。", user: strings.Repeat("上下文窗口限制输入与输出的总令牌数。", 8) + "请只回答：好的。"},
		{id: "code", system: "Review code briefly.", user: "Review this Go snippet and reply OK: func score(s string) int { if s == \"\" { return 0 }; return len([]rune(s)) }"},
		{id: "history", system: "Continue the conversation.", user: "Confirm the deployment state in one word.", history: []domain.Message{
			{ID: "h1", Role: "user", Content: "The staging rollout finished at 09:00."},
			{ID: "h2", Role: "assistant", Content: "I will check the smoke-test result next."},
			{ID: "h3", Role: "user", Content: "All smoke tests passed at 09:12."},
			{ID: "h4", Role: "assistant", Content: "Staging is ready for review."},
		}},
		{id: "rag", system: "Use retrieved evidence only when relevant.", user: "What happened before deployment? Reply briefly.", knowledge: []domain.RetrievedDocumentChunk{{
			Document: domain.Document{Title: "Release checklist"},
			Chunk:    domain.DocumentChunk{ID: "calibration-chunk", Content: "Before deployment, run smoke tests and verify the rollback plan."},
			Score:    0.9, SourceID: "S1",
		}}},
		{id: "tool_schema", system: "Reply OK without calling a tool.", user: "Reply OK.", withTool: true},
		{id: "near_boundary", system: "Reply OK.", user: strings.Repeat("boundary ", nearWords) + "Reply OK."},
		{id: "preflight_rejection", system: "Reply OK.", user: strings.Repeat("overflow ", max(1, inputBudget)) + "Reply OK.", rejectPreflight: true},
	}
}

func calibrationToolCatalog() (*tool.Catalog, error) {
	properties := make(map[string]any, 12)
	for index := range 12 {
		properties[fmt.Sprintf("field_%02d", index)] = map[string]any{
			"type": "string", "description": "A field extracted from the supplied structured record.",
		}
	}
	return tool.NewCatalog(tool.Binding{
		Descriptor: tool.Descriptor{
			Name: "extract_record", Description: "Extract fields from a structured record for calibration.",
			Parameters: tool.ObjectSchema(properties, []string{"field_00"}),
		},
		Handler: func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
	})
}
