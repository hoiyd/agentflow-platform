package clarification

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestClarificationContract(t *testing.T) {
	catalog, err := tool.NewCatalog(Binding())
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{})
	for _, args := range []string{
		`{"questions":[{"id":"audience","question":"Who is the audience?","options":["Engineers","Managers"]}]}`,
		`{"questions":[{"id":"goal","question":"What is the goal?"}]}`,
	} {
		result := executor.Execute(context.Background(), tool.ExecutionRequest{CallID: "call", Tool: ToolName, Arguments: json.RawMessage(args)})
		if result.Error != nil {
			t.Fatalf("valid: %+v", result)
		}
	}
	for _, args := range []string{
		`{"questions":[]}`,
		`{"questions":[{"id":"a","question":"What?"},{"id":"a","question":"Other?"}]}`,
		`{"questions":[{"id":"a","question":"What?","options":["Same","Same"]}]}`,
		`{"questions":[{"id":"a","question":""}]}`,
		`{"questions":[{"id":"a","question":"What?","unexpected":true}]}`,
		`{"questions":[{"id":"password","question":"Please provide your password"}]}`,
		`{"questions":[{"id":"key","question":"请提供你的 API key"}]}`,
	} {
		result := executor.Execute(context.Background(), tool.ExecutionRequest{CallID: "call", Tool: ToolName, Arguments: json.RawMessage(args)})
		if result.Error == nil {
			t.Fatalf("invalid accepted: %s", args)
		}
	}
	q := []domain.ClarificationQuestion{{ID: "goal", Question: "What outcome?"}}
	for _, a := range []map[string]string{nil, {"other": "answer"}, {"goal": " "}, {"goal": strings.Repeat("x", 8193)}} {
		if ValidateAnswers(q, a) == nil {
			t.Fatalf("invalid answers accepted: %v", a)
		}
	}
	if err := ValidateAnswers(q, map[string]string{"goal": "A short report"}); err != nil {
		t.Fatal(err)
	}
}
