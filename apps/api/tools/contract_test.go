package tools_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	agenttools "agentflow-platform/apps/api/internal/tools"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestDomainResponsesMatchOpenAPIContract(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	run := domain.Run{
		ID: "run-1", AgentID: "agent-1", ConversationID: "conversation-1",
		Status: domain.RunCompleted, VerificationStatus: domain.VerificationNotRequired,
		CreatedAt: now, UpdatedAt: now,
	}
	conversation := domain.Conversation{ID: "conversation-1", Title: "Contract test", CreatedAt: now, UpdatedAt: now}
	summary := domain.RunTraceSummary{RunID: run.ID, Status: run.Status}
	ledger := domain.RunUsageLedger{
		RunID: run.ID, Budget: domain.RuntimeRunBudget{}, Totals: domain.RunUsageTotals{}, Entries: []domain.RunUsageEntry{},
	}

	document := loadContract(t)
	assertSchema(t, document, "Agent", domain.Agent{
		ID: "agent-1", Name: "Agent", Description: "Contract test", SystemPrompt: "Help",
		Tools: []string{}, MemoryEnabled: true, RetrievalEnabled: true, Executor: domain.DefaultAgentExecutor,
		CreatedAt: now, UpdatedAt: now,
	})
	assertSchema(t, document, "ToolInfo", agenttools.ToolInfo{
		Name: "get_current_time", Description: "Current time", Parameters: map[string]any{}, Enabled: true,
	})
	assertSchema(t, document, "Run", run)
	assertSchema(t, document, "RunUsageLedger", ledger)
	assertSchema(t, document, "RunReplay", domain.RunReplay{
		Run: run, Conversation: conversation, Messages: []domain.Message{}, Steps: []domain.CollaborationStep{},
		Summary: summary, UsageLedger: ledger, RunEvents: []domain.RunEvent{},
		VerificationEvidence: []domain.VerificationEvidence{}, VerificationArtifacts: []domain.VerificationArtifact{},
	})
}

func loadContract(t *testing.T) *openapi3.T {
	t.Helper()
	document, err := openapi3.NewLoader().LoadFromFile("../../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("load OpenAPI contract: %v", err)
	}
	if err := document.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI contract: %v", err)
	}
	return document
}

func assertSchema(t *testing.T, document *openapi3.T, name string, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	var payload any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	schema := document.Components.Schemas[name]
	if schema == nil || schema.Value == nil {
		t.Fatalf("OpenAPI schema %q not found", name)
	}
	if err := schema.Value.VisitJSON(payload, openapi3.EnableJSONSchema2020()); err != nil {
		t.Fatalf("%s does not match OpenAPI contract: %v\npayload=%s", name, err, encoded)
	}
}
