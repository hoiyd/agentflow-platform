package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/app/runcompletion"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/planning"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/verification"
)

// Failure inventory: conversation drift must not change the Run's question or
// candidate; absent first evidence and blocked infrastructure remain retryable.
// Missing/ambiguous/tampered pointers, question, output, evidence and plan fail
// closed (409) without consuming an attempt. Storage faults return 500/errors,
// never a successful verification or a swallowed candidate-binding write.
func TestVerificationSubjectOriginalRunAfterConversationAdvances(t *testing.T) {
	for _, mode := range []string{"text", "structured", "plan", "grounded", "relevance", "no_evidence", "structured_no_evidence"} {
		t.Run(mode, func(t *testing.T) {
			f := newVerificationSubjectFixture(t, mode)
			fault := strings.Contains(mode, "no_evidence")
			f.complete(t, fault)
			before, err := f.storage.ListVerificationEvidence(f.run.ID)
			if err != nil || fault && len(before) != 0 || !fault && len(before) != 2 {
				t.Fatalf("first evidence: count=%d err=%v", len(before), err)
			}
			if !fault && before[len(before)-1].Status != domain.VerificationBlocked {
				t.Fatalf("first infrastructure check was not blocked: %+v", before)
			}
			// The stored question deliberately differs from the latest user message
			// before completion, as can happen with a clarified/resumed task.
			events, err := f.storage.ListRunEvents(f.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range events {
				if item.Type == domain.EventCitationResolved && item.Payload["verification_question"] != f.question {
					t.Errorf("exact verification question was not persisted: %+v", item.Payload)
				}
			}
			f.advance(t)
			// Reopen the real database: no process-local subject cache may help.
			if err := f.storage.Close(); err != nil {
				t.Fatal(err)
			}
			f.storage, err = store.NewPostgresStore(f.url)
			if err != nil {
				t.Fatal(err)
			}
			response := f.verify(f.storage, true)
			if response.Code != http.StatusOK {
				t.Fatalf("original Run reverify: %d %s", response.Code, response.Body.String())
			}
			persisted, found, err := f.storage.GetRun(f.run.ID)
			if err != nil || !found || persisted.Status != domain.RunCompleted || persisted.VerificationStatus != domain.VerificationPassed {
				t.Fatalf("fresh infrastructure did not complete original candidate: %+v %v", persisted, err)
			}
			after, err := f.storage.ListVerificationEvidence(f.run.ID)
			if err != nil || len(after) != len(before)+2 {
				t.Fatalf("retry evidence: count=%d err=%v", len(after), err)
			}
			want := verification.SubjectForQuestionAnswer(f.question, f.output).Hash
			if mode == "grounded" {
				want = verification.SubjectForGroundedQuestionAnswer(f.question, f.output, nil, nil).Hash
			}
			for _, item := range after {
				if item.SubjectHash != want || item.Status == domain.VerificationStale {
					t.Fatalf("verification adopted another subject: %+v want=%s", item, want)
				}
			}
			messages, err := f.storage.ListMessages(f.run.ConversationID)
			if err != nil || len(messages) != 4 {
				t.Fatalf("reverify resaved candidate: messages=%d err=%v", len(messages), err)
			}
			if strings.HasPrefix(mode, "structured") {
				r := f.request(http.MethodGet, "result")
				w := httptest.NewRecorder()
				(&Handler{store: f.storage}).getRunResult(w, r)
				var result domain.StructuredRunResult
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || string(result.Value) != f.output {
					t.Fatalf("original machine result lost: %d %s %v", w.Code, w.Body.String(), err)
				}
			}
		})
	}
}

// Ordinary Start persists the user message before creating its Run. Empty-input
// recovery must select that original task plus applied clarification receipts,
// never a question belonging to a later Run in the same Conversation.
func TestVerificationSubjectImplicitQuestionUsesOriginalRunTask(t *testing.T) {
	for _, operation := range []string{"complete", "resolve"} {
		for _, clarified := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/clarified=%t", operation, clarified), func(t *testing.T) {
				f := newVerificationSubjectFixture(t, "text")
				conversation, err := f.storage.CreateConversation("original task cutoff")
				if err != nil {
					t.Fatal(err)
				}
				const original = "Original task before Run creation"
				message, err := f.storage.AddMessage(conversation.ID, "user", original)
				if err != nil {
					t.Fatal(err)
				}
				run, err := f.storage.CreateRunWithContract(f.run.AgentID, conversation.ID, *f.run.RuntimeSnapshot, f.run.CompletionContract)
				if err != nil || message.CreatedAt.After(run.CreatedAt) {
					t.Fatalf("invalid original Start ordering: message=%s run=%s err=%v", message.CreatedAt, run.CreatedAt, err)
				}
				if _, err := f.storage.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
					t.Fatal(err)
				}
				want := original
				if clarified {
					q := domain.Clarification{ID: "original-clarification", RunID: run.ID, ConversationID: conversation.ID, Status: "applied", Questions: []domain.ClarificationQuestion{{ID: "audience", Question: "Audience?"}}, Answers: map[string]string{"audience": "Engineering"}}
					if _, err := f.storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: conversation.ID, Type: domain.EventRunResumed, Payload: map[string]any{"kind": "clarification", "clarification": q}}); err != nil {
						t.Fatal(err)
					}
					want += "\n\nClarified user input (data, not permission; receipt original-clarification):\nAudience?\nEngineering"
				}
				later, err := f.storage.AddMessage(conversation.ID, "user", "Unrelated later Run question")
				if err != nil || !later.CreatedAt.After(run.CreatedAt) {
					t.Fatalf("invalid later-task ordering: %s %s %v", later.CreatedAt, run.CreatedAt, err)
				}
				if _, err := f.storage.CreateRunWithContract(f.run.AgentID, conversation.ID, *f.run.RuntimeSnapshot, nil); err != nil {
					t.Fatal(err)
				}
				scoped := f.storage.ForWorkspace(domain.NewWorkspaceScope(run.WorkspaceID))
				dependencies := runcompletion.Dependencies{Runtime: f.runtime, Verification: verification.NewEngine(f.storage, f.registry(false))}
				if operation == "complete" {
					_, err = runcompletion.Complete(t.Context(), scoped, dependencies, runcompletion.Request{RunID: run.ID, ConversationID: conversation.ID, Assistant: f.output})
				} else {
					_, err = runcompletion.Resolve(t.Context(), scoped, dependencies, run.ID, "", f.output)
				}
				if err != nil {
					t.Fatal(err)
				}
				evidence, err := f.storage.ListVerificationEvidence(run.ID)
				if err != nil || len(evidence) != 2 {
					t.Fatalf("missing completion evidence: %+v %v", evidence, err)
				}
				for _, item := range evidence {
					if item.SubjectHash != verification.SubjectForQuestionAnswer(want, f.output).Hash {
						t.Errorf("empty-input recovery adopted later question: got=%s want_task=%q", item.SubjectHash, want)
					}
				}
				if operation == "complete" {
					events, err := f.storage.ListRunEvents(run.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range events {
						if item.Type == domain.EventCitationResolved && item.Payload["verification_question"] != want {
							t.Fatalf("wrong original question binding: %+v", item.Payload)
						}
					}
				}
			})
		}
	}
}

func TestVerificationSubjectRejectsBrokenBindings(t *testing.T) {
	for _, point := range []string{"missing_pointer", "missing_message", "wrong_role", "foreign_message", "same_content_pointer", "question", "question_whitespace", "missing_question", "output", "hash", "missing_hash", "evidence", "evidence_backdated", "ambiguous_pointer", "plan", "plan_input", "plan_proposal", "plan_identity", "plan_hash"} {
		t.Run(point, func(t *testing.T) {
			mode := "text"
			if strings.HasPrefix(point, "plan") {
				mode = "plan"
			}
			f := newVerificationSubjectFixture(t, mode)
			f.complete(t, false)
			before, err := f.storage.ListVerificationEvidence(f.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch point {
			case "missing_pointer":
				f.exec(t, `DELETE FROM run_events WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID)
			case "missing_message":
				f.exec(t, `UPDATE run_events SET payload=jsonb_set(payload,'{message_id}','"missing"') WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID)
			case "wrong_role":
				f.exec(t, `UPDATE messages SET role='user' WHERE id=$1`, f.candidateID)
			case "foreign_message":
				other, err := f.storage.CreateConversation("foreign")
				if err != nil {
					t.Fatal(err)
				}
				message, err := f.storage.AddMessage(other.ID, "assistant", f.output)
				if err != nil {
					t.Fatal(err)
				}
				f.exec(t, `UPDATE run_events SET payload=jsonb_set(payload,'{message_id}',to_jsonb($2::text)) WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID, message.ID)
			case "question":
				f.exec(t, `UPDATE run_events SET payload=jsonb_set(payload,'{verification_question}','"different question"') WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID)
			case "same_content_pointer":
				message, err := f.storage.AddMessage(f.run.ConversationID, "assistant", f.output)
				if err != nil {
					t.Fatal(err)
				}
				f.exec(t, `UPDATE run_events SET payload=jsonb_set(payload,'{message_id}',to_jsonb($2::text)) WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID, message.ID)
			case "question_whitespace":
				f.exec(t, `UPDATE run_events SET payload=jsonb_set(payload,'{verification_question}',to_jsonb($2::text)) WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID, strings.TrimSpace(f.question))
			case "missing_question":
				f.exec(t, `UPDATE run_events SET payload=payload-'verification_question' WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID)
			case "output":
				f.exec(t, `UPDATE messages SET content='changed answer' WHERE id=$1`, f.candidateID)
			case "hash":
				f.exec(t, `UPDATE run_events SET payload=jsonb_set(payload,'{verification_subject_hash}','"changed"') WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID)
			case "missing_hash":
				f.exec(t, `UPDATE run_events SET payload=payload-'verification_subject_hash' WHERE run_id=$1 AND type='citation.resolved'`, f.run.ID)
			case "evidence":
				f.exec(t, `UPDATE verification_evidence SET subject_hash='changed' WHERE run_id=$1`, f.run.ID)
			case "evidence_backdated":
				f.exec(t, `UPDATE verification_evidence SET subject_hash='changed',started_at=started_at-interval '1 day' WHERE run_id=$1`, f.run.ID)
			case "ambiguous_pointer":
				_, err := f.storage.CreateRunEvent(domain.RunEvent{RunID: f.run.ID, ConversationID: f.run.ConversationID, Type: domain.EventCitationResolved, Payload: map[string]any{"message_id": "other", "protocol_version": domain.RAGCitationProtocolVersion}})
				if err != nil {
					t.Fatal(err)
				}
			case "plan":
				f.exec(t, `DELETE FROM run_events WHERE run_id=$1 AND type=$2`, f.run.ID, domain.EventPlanRecorded)
			case "plan_input", "plan_proposal", "plan_identity", "plan_hash":
				events, err := f.storage.ListRunEvents(f.run.ID)
				if err != nil {
					t.Fatal(err)
				}
				plan := planning.Read(events)
				if plan == nil {
					t.Fatal("missing original plan")
				}
				switch point {
				case "plan_input":
					plan.Input = "another task"
				case "plan_proposal":
					plan.Proposal = "another proposal"
				case "plan_identity":
					plan.RunID = "another-run"
				}
				plan.Hash = planning.Hash(*plan)
				if point == "plan_hash" {
					plan.Hash = "changed"
				}
				encoded, err := json.Marshal(plan)
				if err != nil {
					t.Fatal(err)
				}
				f.exec(t, `UPDATE run_events SET payload=$2::jsonb WHERE run_id=$1 AND type=$3`, f.run.ID, string(encoded), domain.EventPlanRecorded)
			}
			response := f.verify(f.storage, true)
			if response.Code != http.StatusConflict {
				t.Fatalf("%s accepted: %d %s", point, response.Code, response.Body.String())
			}
			after, err := f.storage.ListVerificationEvidence(f.run.ID)
			if err != nil || len(after) != len(before) {
				t.Fatalf("conflict consumed attempt: %d -> %d %v", len(before), len(after), err)
			}
		})
	}
}

func TestVerificationSubjectRejectedPlanCannotAdoptLaterAnswer(t *testing.T) {
	f := newVerificationSubjectFixture(t, "rejected_plan")
	f.complete(t, false)
	events, err := f.storage.ListRunEvents(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan := planning.Read(events)
	if plan == nil {
		t.Fatal("missing original plan")
	}
	f.advance(t)
	response := f.verify(f.storage, true)
	var result struct {
		Run      domain.Run            `json:"run"`
		Decision verification.Decision `json:"decision"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || result.Decision.AllowCompletion || result.Decision.SubjectHash != verification.SubjectForQuestionAnswer(f.question, f.output).Hash || result.Run.Status != domain.RunFailed {
		t.Fatalf("unrelated answer bypassed rejected plan: %d %s", response.Code, response.Body.String())
	}
	events, err = f.storage.ListRunEvents(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := planning.Read(events)
	if current == nil || current.Hash != plan.Hash || current.Input != f.question || current.Proposal != f.output {
		t.Fatalf("reverify changed proposal: %+v", current)
	}
	if err := planning.Validate(result.Run, current, current.Hash, result.Run.UpdatedAt); err == nil {
		t.Fatal("rejected proposal became executable")
	}
}

func TestVerificationSubjectLatestRevisionWithinSameRun(t *testing.T) {
	f := newVerificationSubjectFixture(t, "text")
	f.complete(t, false)
	// Seed the resumed admission state; candidate persistence, verifiers,
	// evidence, settlement and the subsequent HTTP reverify remain real.
	if _, err := f.storage.UpdateRunStatus(f.run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	f.question, f.output = "Original task with corrected details", "Original revised answer."
	f.complete(t, false)
	f.advance(t)
	response := f.verify(f.storage, true)
	if response.Code != http.StatusOK {
		t.Fatalf("Run-local revision lost: %d %s", response.Code, response.Body.String())
	}
	run, found, err := f.storage.GetRun(f.run.ID)
	if err != nil || !found || run.Status != domain.RunCompleted {
		t.Fatalf("Run-local revision did not complete: %+v %v", run, err)
	}
	evidence, err := f.storage.ListVerificationEvidence(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := verification.SubjectForQuestionAnswer(f.question, f.output).Hash
	for _, item := range evidence {
		if item.Attempt == 3 && (item.SubjectHash != want || item.Status != domain.VerificationPassed) {
			t.Fatalf("wrong Run-local revision verified: %+v", item)
		}
	}
}

// A durable revision is authoritative even if verification fails before its
// first result. Historical evidence must not look like a tampered current result.
func TestVerificationSubjectRevisionBeforeFirstEvidenceRecovers(t *testing.T) {
	for _, point := range []string{"append", "append_current", "engine_evidence"} {
		t.Run(point, func(t *testing.T) {
			f := newVerificationSubjectFixture(t, "text")
			f.complete(t, false)
			if _, err := f.storage.UpdateRunStatus(f.run.ID, domain.RunRunning, ""); err != nil {
				t.Fatal(err)
			}
			f.question, f.output = "Original revised task", "Original revised candidate B"
			fault := verificationSubjectFaultStore{Store: f.storage, point: point}
			_, err := runcompletion.Complete(t.Context(), fault.ForWorkspace(domain.NewWorkspaceScope(f.run.WorkspaceID)), runcompletion.Dependencies{Runtime: f.runtime, Verification: verification.NewEngine(fault, f.registry(true))}, runcompletion.Request{RunID: f.run.ID, ConversationID: f.run.ConversationID, UserInput: f.question, Assistant: f.output})
			if err == nil || !strings.Contains(err.Error(), "subject storage fault") {
				t.Fatalf("revision fault lost: %v", err)
			}
			f.advance(t)
			response := f.verify(f.storage, true)
			var result struct {
				Run      domain.Run            `json:"run"`
				Decision verification.Decision `json:"decision"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			want := verification.SubjectForQuestionAnswer(f.question, f.output).Hash
			if response.Code != 200 || !result.Decision.AllowCompletion || result.Decision.SubjectHash != want || result.Run.Status != domain.RunCompleted {
				t.Fatalf("revision stranded by historical evidence: %d %s", response.Code, response.Body.String())
			}
			evidence, err := f.storage.ListVerificationEvidence(f.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			passed := 0
			for _, item := range evidence {
				if item.SubjectHash == want && item.Status == domain.VerificationPassed {
					passed++
				}
			}
			if passed != 2 {
				t.Fatalf("revision lacks fresh results: %+v", evidence)
			}
			t.Logf("revision recovered: run=%s binding_subject=%s attempt=%d fault=%s", f.run.ID, want, result.Decision.Attempt, point)
		})
	}
}

// Hold A's real verifier while a real Resume tries to generate B. Without shared
// admission Resume can persist/reject B before A's passing result completes it.
func TestVerificationSubjectConcurrentResumeCannotReplaceVerifyingCandidate(t *testing.T) {
	f := newVerificationSubjectFixture(t, "relevance")
	f.complete(t, false)
	if _, err := f.storage.CreateCollaborationStep(domain.CollaborationStep{RunID: f.run.ID, ConversationID: f.run.ConversationID, AgentID: f.run.AgentID, Role: "observe", Status: domain.CollaborationStepCompleted, Iteration: 1, Input: f.question, Output: "ready for recovery"}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	var embeddings atomic.Int32
	registry := verification.NewRegistry(verification.Options{AllowedHTTPHosts: []string{f.healthURL}, AnswerRelevanceEmbedder: func(ctx context.Context, input string) (verification.AnswerRelevanceEmbedding, error) {
		if embeddings.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return verification.AnswerRelevanceEmbedding{}, ctx.Err()
			}
		}
		vector := []float64{0, 1}
		if input == strings.TrimSpace(f.question) || strings.Contains(input, "Original answer") {
			vector = []float64{1, 0}
		}
		return verification.AnswerRelevanceEmbedding{Vector: vector, Provider: "fixture", Model: "fixture", Dimensions: 2}, nil
	}})
	var calls atomic.Int32
	client := verificationReplacementClient{Client: newLocalFallbackOpenAIClientForTest(), calls: &calls}
	runtime := newRuntimeForTest(agent.RuntimeOptions{Store: f.storage}, client)
	controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 2, QueueSize: 2, WaitTimeout: 5 * time.Second})
	handler := &Handler{store: f.storage, agentRuntime: runtime, verification: verification.NewEngine(f.storage, registry), runController: controller}
	verifyDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		handler.verifyRun(w, f.request(http.MethodPost, "verify"))
		verifyDone <- w
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("original verifier did not reach barrier")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	resume := f.request(http.MethodPost, "resume").WithContext(ctx)
	resume.Body = io.NopCloser(bytes.NewReader([]byte(`{"user_input":"resume with replacement"}`)))
	resumeDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		handler.resumeRun(w, resume)
		resumeDone <- w
	}()
	var resumed *httptest.ResponseRecorder
	select {
	case resumed = <-resumeDone:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent Resume did not settle")
	}
	unblock.Do(func() { close(release) })
	var verified *httptest.ResponseRecorder
	select {
	case verified = <-verifyDone:
	case <-time.After(10 * time.Second):
		t.Fatal("original verification did not settle")
	}
	if calls.Load() != 0 {
		t.Errorf("Resume reached replacement provider while A held verification: calls=%d response=%s", calls.Load(), resumed.Body.String())
	}
	if verified.Code != 200 {
		t.Fatalf("original verification failed: %d %s", verified.Code, verified.Body.String())
	}
	messages, err := f.storage.ListMessages(f.run.ConversationID)
	if err != nil || len(messages) != 2 || messages[1].Content != f.output {
		t.Fatalf("old subject completed a replacement candidate: messages=%+v err=%v", messages, err)
	}
	current, _, err := f.storage.GetRun(f.run.ID)
	if err != nil || current.Status != domain.RunCompleted {
		t.Fatalf("original candidate not completed: %+v %v", current, err)
	}
	t.Logf("concurrent barrier: run=%s provider_calls=%d verify_status=%d original_subject=%s", f.run.ID, calls.Load(), verified.Code, verification.SubjectForQuestionAnswer(f.question, f.output).Hash)
}

type verificationReplacementClient struct {
	provider.Client
	calls *atomic.Int32
}

func (c verificationReplacementClient) WithRuntimeIdentity(identity provider.RuntimeIdentity) provider.Client {
	return verificationReplacementClient{Client: c.Client.WithRuntimeIdentity(identity), calls: c.calls}
}

func (c verificationReplacementClient) CompletePreparedText(context.Context, provider.PreparedText) (provider.TextCompletion, error) {
	c.calls.Add(1)
	return provider.TextCompletion{Text: `{"decision":"stop","reason":"fixture","final_answer":"replacement-invalid answer"}`, Model: c.RuntimeIdentity().Model, Usage: provider.Usage{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}}, nil
}

func (c verificationReplacementClient) CompleteTextDetailed(ctx context.Context, _, _ string) (provider.TextCompletion, error) {
	return c.CompletePreparedText(ctx, provider.PreparedText{})
}

func TestVerificationSubjectStorageErrors(t *testing.T) {
	for _, point := range []string{"messages", "events", "evidence", "append"} {
		t.Run(point, func(t *testing.T) {
			f := newVerificationSubjectFixture(t, "text")
			f.complete(t, false)
			response := f.verify(verificationSubjectFaultStore{Store: f.storage, point: point}, true)
			if response.Code != http.StatusInternalServerError {
				t.Fatalf("%s fault returned %d %s", point, response.Code, response.Body.String())
			}
		})
	}
}

func TestVerificationSubjectBindingWriteRequired(t *testing.T) {
	f := newVerificationSubjectFixture(t, "text")
	fault := verificationSubjectFaultStore{Store: f.storage, point: "binding"}
	_, err := runcompletion.Complete(t.Context(), fault.ForWorkspace(domain.NewWorkspaceScope(f.run.WorkspaceID)), runcompletion.Dependencies{Runtime: f.runtime, Verification: verification.NewEngine(fault, f.registry(false))}, runcompletion.Request{RunID: f.run.ID, ConversationID: f.run.ConversationID, UserInput: f.question, Assistant: f.output})
	if err == nil || !strings.Contains(err.Error(), "subject storage fault") {
		t.Fatalf("candidate binding write error swallowed: %v", err)
	}
	evidence, err := f.storage.ListVerificationEvidence(f.run.ID)
	if err != nil || len(evidence) != 0 {
		t.Fatalf("unbound candidate verified: %+v %v", evidence, err)
	}
}

func TestVerificationSubjectUsesExplicitCompletion(t *testing.T) {
	f := newVerificationSubjectFixture(t, "text")
	lifecycle := &verificationSubjectLifecycle{Runtime: f.runtime}
	_, err := runcompletion.Complete(t.Context(), f.storage.ForWorkspace(domain.NewWorkspaceScope(f.run.WorkspaceID)), runcompletion.Dependencies{Runtime: lifecycle, Verification: verification.NewEngine(f.storage, f.registry(true))}, runcompletion.Request{RunID: f.run.ID, ConversationID: f.run.ConversationID, UserInput: f.question, Assistant: f.output})
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle.workerCompletions != 0 || lifecycle.verifiedCompletions != 1 {
		t.Fatalf("verification success used worker settlement: worker=%d verified=%d", lifecycle.workerCompletions, lifecycle.verifiedCompletions)
	}
}

type verificationSubjectLifecycle struct {
	*agent.Runtime
	workerCompletions, verifiedCompletions int
}

func (r *verificationSubjectLifecycle) CompleteRun(id string) (domain.Run, error) {
	r.workerCompletions++
	return r.Runtime.CompleteRun(id)
}

func (r *verificationSubjectLifecycle) CompleteVerifiedRun(id string) (domain.Run, error) {
	r.verifiedCompletions++
	return r.Runtime.CompleteVerifiedRun(id)
}

type verificationSubjectFixture struct {
	storage                                             *store.PostgresStore
	runtime                                             *agent.Runtime
	run                                                 domain.Run
	url, healthURL, question, output, mode, candidateID string
}

func newVerificationSubjectFixture(t *testing.T, mode string) *verificationSubjectFixture {
	t.Helper()
	f := &verificationSubjectFixture{url: pgfixture.DatabaseURL(t), mode: mode, question: "  Original task\nwith exact details.  ", output: "Original answer with exact details."}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(server.Close)
	f.healthURL = server.URL
	var err error
	f.storage, err = store.NewPostgresStore(f.url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.storage.Close() })
	conversation, err := f.storage.CreateConversation("verification subject integration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.storage.AddMessage(conversation.ID, "user", "latest history is not the resolved task"); err != nil {
		t.Fatal(err)
	}
	spec := domain.VerifierSpec{ID: "candidate", Type: domain.VerifierTextConstraints, Required: true, Config: map[string]any{"required_phrases": []string{"Original"}}}
	snapshot := testRuntimeSnapshot()
	var output *domain.StructuredOutputConfig
	switch strings.TrimSuffix(mode, "_no_evidence") {
	case "structured":
		f.output = `{"summary":"Original"}`
		spec.Type, spec.Config = domain.VerifierJSONSchema, map[string]any{"schema": map[string]any{"type": "object"}}
		output = &domain.StructuredOutputConfig{Mode: "local", VerifierID: "candidate"}
	case "plan", "rejected_plan":
		snapshot.ExecutionConstraint = domain.ExecutionPlanOnly
		if mode == "rejected_plan" {
			f.output = "Original Proposed report"
			spec.Config = map[string]any{"forbidden_phrases": []string{"Proposed report"}}
		}
	case "grounded":
		f.output = "insufficient evidence"
		spec.Type, spec.Config = domain.VerifierGroundedAnswer, map[string]any{"no_answer_phrases": []string{"insufficient evidence"}}
	case "relevance":
		spec.Type, spec.Config = domain.VerifierAnswerRelevance, map[string]any{"minimum_score": 0.5, "minimum_answer_characters": 10}
	}
	policy := domain.VerificationPolicy{MaxAttempts: 3, OnExhausted: domain.VerificationWaitForUser}
	if mode == "rejected_plan" {
		policy.MaxAttempts, policy.OnExhausted = 2, domain.VerificationFailRun
	}
	contract, err := f.registry(false).FreezeContract(&domain.CompletionContract{Output: output, Verifiers: []domain.VerifierSpec{spec, {ID: "infrastructure", Type: domain.VerifierHTTP, Required: true, Config: map[string]any{"url": server.URL}}}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	f.run, err = f.storage.CreateRunWithContract("agent_planner", conversation.ID, snapshot, contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.storage.UpdateRunStatus(f.run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	contractJSON, err := json.Marshal(contract)
	if err != nil {
		t.Fatal(err)
	}
	snapshotHash, err := verification.SnapshotHash(f.run.RuntimeSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture: run=%s workspace=%s conversation=%s snapshot_hash=%s mode=%s question=%q candidate=%q contract=%s; disposable PG, simulated Runtime, local HTTP only", f.run.ID, f.run.WorkspaceID, conversation.ID, snapshotHash, mode, f.question, f.output, contractJSON)
	f.runtime = newRuntimeForTest(agent.RuntimeOptions{Store: f.storage}, nil)
	return f
}

func (f *verificationSubjectFixture) registry(healthy bool) *verification.Registry {
	options := verification.Options{AnswerRelevanceEmbedder: func(_ context.Context, input string) (verification.AnswerRelevanceEmbedding, error) {
		vector := []float64{0, 1}
		if input == strings.TrimSpace(f.question) || strings.Contains(input, "Original answer") {
			vector = []float64{1, 0}
		}
		return verification.AnswerRelevanceEmbedding{Vector: vector, Provider: "fixture", Model: "fixture", Dimensions: 2}, nil
	}}
	if healthy {
		options.AllowedHTTPHosts = []string{f.healthURL}
	}
	return verification.NewRegistry(options)
}

func (f *verificationSubjectFixture) complete(t *testing.T, failEvidence bool) {
	t.Helper()
	var storage store.Store = f.storage
	if failEvidence {
		storage = verificationSubjectFaultStore{Store: storage, point: "append"}
	}
	chunk, err := runcompletion.Complete(t.Context(), storage.ForWorkspace(domain.NewWorkspaceScope(f.run.WorkspaceID)), runcompletion.Dependencies{Runtime: f.runtime, Verification: verification.NewEngine(storage, f.registry(false))}, runcompletion.Request{RunID: f.run.ID, ConversationID: f.run.ConversationID, UserInput: f.question, Assistant: f.output})
	if failEvidence {
		if err == nil {
			t.Fatal("first evidence storage failure was not propagated")
		}
		messages, listErr := f.storage.ListMessages(f.run.ConversationID)
		if listErr != nil || len(messages) != 2 {
			t.Fatalf("failed first verification lost candidate: %+v %v", messages, listErr)
		}
		f.candidateID = messages[1].ID
	} else {
		if err != nil {
			t.Fatal(err)
		}
		f.candidateID = chunk.MessageID
	}
}

func (f *verificationSubjectFixture) advance(t *testing.T) {
	t.Helper()
	for _, message := range []struct{ role, content string }{{"user", "a later unrelated task"}, {"assistant", "a later unrelated answer"}} {
		if _, err := f.storage.AddMessage(f.run.ConversationID, message.role, message.content); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *verificationSubjectFixture) request(method, action string) *http.Request {
	r := httptest.NewRequest(method, "/api/runs/"+f.run.ID+"/"+action, nil)
	r.SetPathValue("id", f.run.ID)
	r.Header.Set("X-Workspace-ID", f.run.WorkspaceID)
	return r
}

func (f *verificationSubjectFixture) verify(storage store.Store, healthy bool) *httptest.ResponseRecorder {
	runtime := newRuntimeForTest(agent.RuntimeOptions{Store: storage}, nil)
	handler := &Handler{store: storage, agentRuntime: runtime, verification: verification.NewEngine(storage, f.registry(healthy)), runController: concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})}
	w := httptest.NewRecorder()
	handler.verifyRun(w, f.request(http.MethodPost, "verify"))
	return w
}

func (f *verificationSubjectFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("pgx", f.url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	result, err := db.Exec(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count == 0 {
		t.Fatalf("fault mutation did not affect persisted rows: count=%d err=%v", count, err)
	}
}

type verificationSubjectFaultStore struct {
	store.Store
	point string
}

func (s verificationSubjectFaultStore) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	return verificationSubjectFaultScope{s.Store.ForWorkspace(scope), s.point}
}

func (s verificationSubjectFaultStore) AppendVerificationRecord(record domain.VerificationRecord) error {
	if s.point == "append" || s.point == "append_current" && record.Evidence.Status != domain.VerificationStale {
		return errors.New("subject storage fault: append")
	}
	return s.Store.AppendVerificationRecord(record)
}

func (s verificationSubjectFaultStore) ListVerificationEvidence(id string) ([]domain.VerificationEvidence, error) {
	if s.point == "engine_evidence" {
		return nil, errors.New("subject storage fault: engine evidence")
	}
	return s.Store.ListVerificationEvidence(id)
}

type verificationSubjectFaultScope struct {
	store.WorkspaceStore
	point string
}

func (s verificationSubjectFaultScope) ListMessages(id string) ([]domain.Message, error) {
	if s.point == "messages" {
		return nil, errors.New("subject storage fault: messages")
	}
	return s.WorkspaceStore.ListMessages(id)
}

func (s verificationSubjectFaultScope) ListRunEvents(id string) ([]domain.RunEvent, error) {
	if s.point == "events" {
		return nil, errors.New("subject storage fault: events")
	}
	return s.WorkspaceStore.ListRunEvents(id)
}

func (s verificationSubjectFaultScope) ListVerificationEvidence(id string) ([]domain.VerificationEvidence, error) {
	if s.point == "evidence" {
		return nil, errors.New("subject storage fault: evidence")
	}
	return s.WorkspaceStore.ListVerificationEvidence(id)
}

func (s verificationSubjectFaultScope) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	if s.point == "binding" && item.Type == domain.EventCitationResolved {
		return domain.RunEvent{}, errors.New("subject storage fault: binding")
	}
	return s.WorkspaceStore.CreateRunEvent(item)
}
