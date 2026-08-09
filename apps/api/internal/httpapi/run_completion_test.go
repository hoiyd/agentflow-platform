package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

type recordingMemoryCurationQueue struct {
	jobs []memorypkg.CurationJob
}

type listMessagesErrorStore struct {
	store.Store
	err error
}

func (s listMessagesErrorStore) ListMessages(string) ([]domain.Message, error) {
	return nil, s.err
}

func TestCompleteStreamingRunRequiresFreshPassingEvidence(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("verified completion")
	registry := verification.NewRegistry(verification.Options{})
	contract, err := registry.FreezeContract(&domain.CompletionContract{
		ID: "contract_json", Verifiers: []domain.VerifierSpec{{
			ID: "response-schema", Type: domain.VerifierJSONSchema, Required: true,
			Config: map[string]any{"schema": map[string]any{
				"type": "object", "properties": map[string]any{"status": map[string]any{"const": "ok"}}, "required": []any{"status"},
			}},
		}},
		Policy: domain.VerificationPolicy{Mode: domain.VerificationAllMustPass, MaxAttempts: 1, OnExhausted: domain.VerificationWaitForUser},
	})
	if err != nil {
		t.Fatalf("freeze contract: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	runtime := agent.NewRuntime(agent.RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{store: fileStore, agentRuntime: runtime, verification: verification.NewEngine(fileStore, registry)}
	response := httptest.NewRecorder()

	ok := handler.completeStreamingRun(response, response, context.Background(), runCompletionRequest{
		RunID: run.ID, ConversationID: conversation.ID, Assistant: `{"status":"wrong"}`,
	})
	if !ok {
		t.Fatalf("verification rejection should be a durable completion decision: %s", response.Body.String())
	}
	updated, found, err := fileStore.GetRun(run.ID)
	if err != nil || !found || updated.Status != domain.RunWaitingForUser || updated.VerificationStatus != domain.VerificationFailed {
		t.Fatalf("run bypassed completion gate: run=%#v found=%v err=%v", updated, found, err)
	}
	if !strings.Contains(response.Body.String(), `"status":"waiting_for_user"`) {
		t.Fatalf("terminal SSE did not expose gate decision: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"verification_status":"failed"`) {
		t.Fatalf("terminal SSE did not expose verification status: %s", response.Body.String())
	}
	if evidence, err := fileStore.ListVerificationEvidence(run.ID); err != nil || len(evidence) != 1 || evidence[0].Status != domain.VerificationFailed {
		t.Fatalf("missing failed evidence: %#v err=%v", evidence, err)
	}
}

func TestCompleteStreamingRunProvidesQuestionToAnswerRelevanceVerifier(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("answer relevance")
	if _, err := fileStore.AddMessage(conversation.ID, "user", "What are your opening hours?"); err != nil {
		t.Fatalf("add user message: %v", err)
	}
	registry := verification.NewRegistry(verification.Options{AnswerRelevanceEmbedder: func(_ context.Context, input string) (verification.AnswerRelevanceEmbedding, error) {
		vector := []float64{1, 0}
		if strings.Contains(strings.ToLower(input), "open from") {
			vector = []float64{0.98, 0.1}
		}
		return verification.AnswerRelevanceEmbedding{Vector: vector, Model: "test-embedding", Provider: "test", Dimensions: len(vector)}, nil
	}})
	contract, err := registry.FreezeContract(&domain.CompletionContract{
		Verifiers: []domain.VerifierSpec{{
			ID: "answer-relevance", Type: domain.VerifierAnswerRelevance, Required: true,
			Config: map[string]any{"minimum_score": 0.65, "minimum_answer_characters": 10},
		}},
		Policy: domain.VerificationPolicy{Mode: domain.VerificationAllMustPass, MaxAttempts: 1, OnExhausted: domain.VerificationFailRun},
	})
	if err != nil {
		t.Fatalf("freeze contract: %v", err)
	}
	run, _ := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	_, _ = fileStore.UpdateRunStatus(run.ID, domain.RunRunning, "")
	runtime := agent.NewRuntime(agent.RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{store: fileStore, agentRuntime: runtime, verification: verification.NewEngine(fileStore, registry)}
	response := httptest.NewRecorder()

	if !handler.completeStreamingRun(response, response, context.Background(), runCompletionRequest{
		RunID: run.ID, ConversationID: conversation.ID,
		Assistant: "We are open from 9am to 10pm every day.",
	}) {
		t.Fatalf("complete verified run: %s", response.Body.String())
	}
	completed, _, _ := fileStore.GetRun(run.ID)
	if completed.Status != domain.RunCompleted || completed.VerificationStatus != domain.VerificationPassed {
		t.Fatalf("answer relevance did not open completion gate: %#v", completed)
	}
	evidence, err := fileStore.ListVerificationEvidence(run.ID)
	if err != nil || len(evidence) != 1 || evidence[0].Details["algorithm"] != "cosine_similarity" {
		t.Fatalf("answer relevance evidence missing: %#v err=%v", evidence, err)
	}
}

func TestResolveRunCompletionReturnsQuestionLookupError(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("question lookup failure")
	registry := verification.NewRegistry(verification.Options{})
	contract, err := registry.FreezeContract(&domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
		ID: "response-text", Type: domain.VerifierTextConstraints, Required: true,
		Config: map[string]any{"min_characters": 1},
	}}})
	if err != nil {
		t.Fatalf("freeze contract: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	want := fmt.Errorf("list messages failed")
	runtime := agent.NewRuntime(agent.RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{
		store: listMessagesErrorStore{Store: fileStore, err: want}, agentRuntime: runtime,
		verification: verification.NewEngine(fileStore, registry),
	}
	if _, err := handler.resolveRunCompletion(context.Background(), run.ID, "", "candidate"); err != want {
		t.Fatalf("expected question lookup error, got %v", err)
	}
}

func TestVerifyRunRetriesRecoverableEvidenceAndCompletes(t *testing.T) {
	serviceHealthy := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !serviceHealthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("reverify")
	registry := verification.NewRegistry(verification.Options{})
	contract, err := registry.FreezeContract(&domain.CompletionContract{
		ID: "contract_http", Verifiers: []domain.VerifierSpec{{
			ID: "health", Type: domain.VerifierHTTP, Required: true,
			Config: map[string]any{"method": http.MethodGet, "url": server.URL, "expected_status": http.StatusOK},
		}},
		Policy: domain.VerificationPolicy{Mode: domain.VerificationAllMustPass, MaxAttempts: 2, OnExhausted: domain.VerificationFailRun},
	})
	if err != nil {
		t.Fatalf("freeze contract: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	runtime := agent.NewRuntime(agent.RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{store: fileStore, agentRuntime: runtime, verification: verification.NewEngine(fileStore, registry)}
	first := httptest.NewRecorder()
	if !handler.completeStreamingRun(first, first, context.Background(), runCompletionRequest{
		RunID: run.ID, ConversationID: conversation.ID, Assistant: " candidate ",
	}) {
		t.Fatalf("first verification failed unexpectedly: %s", first.Body.String())
	}
	failed, _, _ := fileStore.GetRun(run.ID)
	if failed.Status != domain.RunFailedRecoverable {
		t.Fatalf("expected recoverable verification failure, got %#v", failed)
	}

	serviceHealthy = true
	request := httptest.NewRequest(http.MethodPost, "/api/runs/"+run.ID+"/verify", nil)
	request.SetPathValue("id", run.ID)
	response := httptest.NewRecorder()
	handler.verifyRun(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("reverify: status=%d body=%s", response.Code, response.Body.String())
	}
	completed, _, _ := fileStore.GetRun(run.ID)
	if completed.Status != domain.RunCompleted || completed.VerificationStatus != domain.VerificationPassed {
		t.Fatalf("fresh evidence did not open completion gate: %#v", completed)
	}
	if evidence, err := fileStore.ListVerificationEvidence(run.ID); err != nil || len(evidence) != 2 || evidence[1].Status != domain.VerificationPassed {
		t.Fatalf("unexpected retry evidence: %#v err=%v", evidence, err)
	}
}

func (q *recordingMemoryCurationQueue) Enqueue(job memorypkg.CurationJob) error {
	q.jobs = append(q.jobs, job)
	return nil
}

func TestCompleteStreamingRunPersistsMessageAndCompletesRun(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("completion test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	runtime := agent.NewRuntime(agent.RuntimeOptions{
		Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest(),
	})
	memoryCuration := &recordingMemoryCurationQueue{}
	handler := &Handler{store: fileStore, agentRuntime: runtime, memoryCuration: memoryCuration}
	response := httptest.NewRecorder()
	userMessage := domain.Message{
		ID: "msg_user", ConversationID: conversation.ID, Role: "user",
		Content: "Remember that AgentFlow uses typed events.",
	}

	ok := handler.completeStreamingRun(response, response, context.Background(), runCompletionRequest{
		RunID: run.ID, ConversationID: conversation.ID, Assistant: "Completed response.",
		UserMessage: &userMessage,
	})
	if !ok {
		t.Fatalf("complete streaming run failed: %s", response.Body.String())
	}

	completed, found, err := fileStore.GetRun(run.ID)
	if err != nil || !found || completed.Status != domain.RunCompleted {
		t.Fatalf("unexpected completed run: run=%#v found=%v err=%v", completed, found, err)
	}
	messages, err := fileStore.ListMessages(conversation.ID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 || messages[0].Role != "assistant" || messages[0].Content != "Completed response." {
		t.Fatalf("unexpected persisted messages: %#v", messages)
	}
	if len(memoryCuration.jobs) != 1 || memoryCuration.jobs[0].Message.ID != userMessage.ID || memoryCuration.jobs[0].Message.Role != "user" {
		t.Fatalf("completion should curate only the user message: %#v", memoryCuration.jobs)
	}
}

func TestCompleteStreamingRunPersistsOnlyCitationsSelectedForModelContext(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, _ := fileStore.CreateConversation("citation completion")
	run, _ := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	_, _ = fileStore.UpdateRunStatus(run.ID, domain.RunRunning, "")
	sources := []domain.RAGCitation{
		{SourceID: "S1", DocumentID: "doc-1", DocumentTitle: "Selected", ChunkID: "chunk-1"},
		{SourceID: "S2", DocumentID: "doc-2", DocumentTitle: "Budget excluded", ChunkID: "chunk-2"},
	}
	_, _ = fileStore.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventRetrievalCompleted, Payload: map[string]any{"citation_sources": sources}})
	_, _ = fileStore.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventContextAssembled, Payload: map[string]any{
		"manifest": domain.ContextManifest{Entries: []domain.ContextManifestEntry{
			{Source: "knowledge", CitationSourceID: "S1", Selected: true},
			{Source: "knowledge", CitationSourceID: "S2", Selected: false},
		}},
	}})

	runtime := agent.NewRuntime(agent.RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})
	handler := &Handler{store: fileStore, agentRuntime: runtime}
	response := httptest.NewRecorder()
	if !handler.completeStreamingRun(response, response, context.Background(), runCompletionRequest{
		RunID: run.ID, ConversationID: conversation.ID, Assistant: "Supported [S1], excluded [S2], invented [S9].",
	}) {
		t.Fatalf("complete streaming run: %s", response.Body.String())
	}
	messages, _ := fileStore.ListMessages(conversation.ID)
	if len(messages) != 1 || len(messages[0].Citations) != 1 || messages[0].Citations[0].SourceID != "S1" {
		t.Fatalf("unexpected persisted citations: %#v", messages)
	}
	if !strings.Contains(response.Body.String(), `"citations":[{"source_id":"S1"`) || !strings.Contains(response.Body.String(), `"invalid_citation_ids":["S2","S9"]`) {
		t.Fatalf("terminal SSE did not expose citation resolution: %s", response.Body.String())
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	last := events[len(events)-2]
	if last.Type != domain.EventCitationResolved || !strings.Contains(formatValueForTest(last.Payload), "rag-citation-v1") {
		t.Fatalf("citation resolution was not traced: %#v", events)
	}
}

func formatValueForTest(value any) string {
	return fmt.Sprintf("%v", value)
}
