package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseAutonomousDecision(t *testing.T) {
	decision := parseAutonomousDecision(`{"decision":"stop","reason":"done","final_answer":"Complete."}`)
	if !decision.ValidJSON {
		t.Fatal("expected valid decision JSON")
	}
	if decision.Decision != "stop" || decision.Reason != "done" || decision.FinalAnswer != "Complete." {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestParseAutonomousDecisionAskUser(t *testing.T) {
	decision := parseAutonomousDecision(`{"decision":"ask_user","reason":"missing project name","question":"What project should this status update describe?","final_answer":""}`)
	if !decision.ValidJSON {
		t.Fatal("expected valid decision JSON")
	}
	if decision.Decision != "ask_user" || decision.Question == "" {
		t.Fatalf("unexpected ask_user decision: %#v", decision)
	}
}

func TestInferHumanInputNeedFromPlan(t *testing.T) {
	need := inferHumanInputNeed(
		"Task is understood.",
		"Plan:\n1. Cannot proceed without user input about the target customer segment before drafting the launch plan.",
		"Not started.",
		"Review pending.",
		`{"decision":"stop","reason":"done","final_answer":"Complete."}`,
	)
	if !need.Needed {
		t.Fatal("expected human input need")
	}
	if need.Source != "plan" {
		t.Fatalf("expected plan source, got %q", need.Source)
	}
	if !strings.Contains(need.Question, "target customer segment") {
		t.Fatalf("expected generated question to include evidence, got %q", need.Question)
	}
}

func TestInferHumanInputNeedFromChineseReview(t *testing.T) {
	need := inferHumanInputNeed(
		"任务已理解。",
		"先整理已有信息。",
		"已完成初稿。",
		"Review:\n- 需要用户补充目标受众，否则无法继续完善文案。",
		`{"decision":"continue","reason":"next iteration"}`,
	)
	if !need.Needed {
		t.Fatal("expected human input need")
	}
	if need.Source != "review" {
		t.Fatalf("expected review source, got %q", need.Source)
	}
	if !strings.Contains(need.Question, "目标受众") {
		t.Fatalf("expected generated question to include Chinese evidence, got %q", need.Question)
	}
}

func TestInferHumanInputNeedIgnoresNegativeStatement(t *testing.T) {
	need := inferHumanInputNeed(
		"Task is understood.",
		"No additional user input is required; continue with the given constraints.",
		"Draft is complete.",
		"Review: no blocking gaps.",
		`{"decision":"stop","reason":"done","final_answer":"Complete."}`,
	)
	if need.Needed {
		t.Fatalf("did not expect human input need: %#v", need)
	}
}

func TestParseAutonomousDecisionRejectsBadJSON(t *testing.T) {
	decision := parseAutonomousDecision("not json")
	if decision.ValidJSON || decision.Decision != "" {
		t.Fatalf("expected invalid empty decision, got %#v", decision)
	}
}

func TestAutonomousRunStopsAtMaxIterations(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, err := fixtureStore.CreateConversation("Autonomous test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	retriever := &recordingKnowledgeRetriever{}
	runtime := newRuntimeForTest(RuntimeOptions{Store: fixtureStore, RouterMode: RouterModeQuery, KnowledgeRetriever: retriever, Autonomous: AutonomousLimits{
		MaxIterations: 1, MaxRuntime: time.Minute, MaxOutputChars: 60000, MaxToolCalls: 20,
	}}, newLocalFallbackOpenAIClientForTest())
	prepared, err := runtime.PrepareAutonomousRunWithContract(context.Background(), "", conversation.ID, nil)
	if err != nil {
		t.Fatalf("prepare autonomous run: %v", err)
	}

	const task = "Write a concise project update."
	events, errs := runtime.RunAutonomous(context.Background(), prepared, task)
	seenProgress := false
	for event := range events {
		if event.Type == domain.EventRunProgress {
			seenProgress = true
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("run autonomous: %v", err)
	}
	if !seenProgress {
		t.Fatal("expected autonomous progress event")
	}

	steps, err := fixtureStore.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected autonomous steps")
	}
	if steps[0].Iteration != 1 {
		t.Fatalf("expected first step iteration 1, got %d", steps[0].Iteration)
	}
	if steps[len(steps)-1].Role != "final" {
		t.Fatalf("expected final step, got %q", steps[len(steps)-1].Role)
	}
	runEvents, err := fixtureStore.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventpkg.ValidateLifecycle(runEvents); err != nil {
		t.Fatalf("invalid autonomous event lifecycle: %v", err)
	}
	assertRuntimeRetrievalBoundary(t, fixtureStore, prepared.Run.ID, task, retriever.searches)
}

func assertRuntimeRetrievalBoundary(t *testing.T, fixtureStore *fixturestore.Store, runID, query string, searches []domain.DocumentSearch) {
	t.Helper()
	if len(searches) == 0 {
		t.Fatal("expected knowledge retrieval")
	}
	for _, search := range searches {
		if search.Query != query {
			t.Fatalf("stage scaffolding leaked into retrieval query: got %q want %q", search.Query, query)
		}
	}
	events, err := fixtureStore.ListRunEvents(runID)
	if err != nil {
		t.Fatal(err)
	}
	completed := 0
	for _, item := range events {
		if item.Type != domain.EventRetrievalCompleted {
			continue
		}
		completed++
		if item.StageID == "" || item.Payload["query"] != query || item.Payload["query_source"] != string(retrievalQuerySourceUserInput) {
			t.Fatalf("retrieval provenance does not identify the user query and owning stage: %#v", item)
		}
		if item.Payload["chunk_count"] != 0 {
			t.Fatalf("no-match retrieval leaked context: %#v", item.Payload)
		}
		if noMatch, present := item.Payload["rag_no_match"]; present && noMatch != true {
			t.Fatalf("unexpected no-match decision: %#v", item.Payload)
		}
		if sources, ok := item.Payload["citation_sources"].([]domain.RAGCitation); ok && len(sources) > 0 {
			t.Fatalf("no-match retrieval produced citation sources: %#v", item.Payload)
		}
	}
	if completed == 0 {
		t.Fatal("expected completed retrieval evidence")
	}
}
