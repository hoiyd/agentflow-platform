package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: malformed/unbound answers, duplicate/conflicting submit,
// legacy Resume bypass, reload/restart, protocol loss, and budget reset.
// The HTTP handler, Runtime, Binding, generated schema and Postgres are real.
// Completion failure inventory: recovered candidates must retain their message
// identity and serialized result/citation fields without a second save; rejected
// verification must not complete, and passing verification must precede completion.
// Resume must not regenerate a title or sync another user turn to Memory.
// A malformed latest receipt/hash or a newer mismatched candidate must reject
// without saving, verifying or falling back to a historical matching answer,
// including Runs without a Completion Contract.
// Corrupt persisted handoff data and a rejected completion write must also
// preserve the candidate without emitting done or leaving the Run running.
func TestProductionDurableClarification(t *testing.T) {
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		for _, scenario := range []string{"happy", "cancel", "withdraw", "claim_restart", "budget", "corrupt", "legacy_text", "completed_restart", "unsafe_restart", "legacy_invalid", "corrupt_identity", "generic_happy", "generic_claim_restart", "generic_completed_restart", "verified_happy", "verified_completed_restart", "output_happy", "output_completed_restart", "output_generic_completed_restart", "ordinary_output", "binding_latest_mismatch", "binding_malformed_latest", "binding_malformed_hash", "verified_binding_latest_mismatch", "verified_binding_malformed_latest", "verified_binding_malformed_hash", "binding_missing_primary", "verified_binding_missing_primary", "binding_invalid_handoff", "binding_completion_write_failure"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				ordinary := scenario == "ordinary_output"
				structured := ordinary || strings.HasPrefix(scenario, "output_")
				contracted := structured || strings.HasPrefix(scenario, "verified_")
				if structured && mode != "single" {
					t.Skip("structured completion boundary is exercised in Single")
				}
				scenario = strings.TrimPrefix(strings.TrimPrefix(scenario, "output_"), "verified_")
				bindingFault := strings.TrimPrefix(scenario, "binding_")
				if bindingFault == scenario {
					bindingFault = ""
				} else {
					if mode != "single" {
						t.Skip("candidate binding recovery boundary is exercised in Single")
					}
					scenario = "completed_restart"
				}
				generic := strings.HasPrefix(scenario, "generic_")
				scenario = strings.TrimPrefix(scenario, "generic_")
				if (scenario == "legacy_text" || scenario == "legacy_invalid") && mode != "autonomous" {
					t.Skip("legacy text ask is an Autonomous protocol")
				}
				task := "clarification-gate: prepare a report, audience unspecified"
				if ordinary {
					task = "structured-output-gate"
				}
				if scenario == "legacy_text" || scenario == "legacy_invalid" {
					task = "clarification-gate-legacy: prepare a report, audience unspecified"
					if scenario == "legacy_invalid" {
						task += " rejected-credential-question"
					}
				}
				database := pgfixture.DatabaseURL(t)
				root := t.TempDir()
				t.Chdir(root)
				fixture := newBrowserProvider()
				providerServer := httptest.NewServer(http.HandlerFunc(fixture.respond))
				defer providerServer.Close()
				t.Setenv("BROWSER_FIXTURE_KEY", "fixture-only")
				t.Setenv("EMBEDDING_API_KEY", "fixture-only")
				t.Setenv("TAVILY_API_KEY", "")
				routes, _ := json.Marshal(map[string]any{"routes": []any{map[string]any{"id": "clarification-fixture", "model": "fixture-model", "base_url": providerServer.URL, "credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 20, "context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100, "capabilities": map[string]any{"tool_calling": true, "streaming": true, "structured_output": true, "structured_output_format": "openai_json_schema"}, "pricing": map[string]string{"source": "fixture"}}}})
				route := filepath.Join(root, "routes.json")
				if err := os.WriteFile(route, routes, 0600); err != nil {
					t.Fatal(err)
				}
				cfg := config.Load()
				cfg.DatabaseURL = database
				cfg.ModelRouteConfigPath = route
				cfg.ToolConfigPath = filepath.Join(root, "tools.json")
				cfg.TrustedSkillDirectories = ""
				cfg.AuthMode = "local"
				cfg.SandboxEnabled = false
				cfg.EmbeddingBaseURL = providerServer.URL
				cfg.EmbeddingModel = "fixture-embedding"
				cfg.EmbeddingDimensions = 1536
				cfg.ContextCompactionMode = "off"
				cfg.MemoryAdaptiveExtractionMode = "off"
				cfg.RerankerMode = "heuristic"
				cfg.RouterMode = "query_match"
				cfg.AutonomousMaxIterations = 1
				cfg.ModelRetryMaxAttempts = 1
				cfg.ModelRequestsPerMinute = 0
				cfg.ModelTokensPerMinute = 0
				cfg.ModelRequestCaptureMode = "redacted"
				cfg.RunMaxModelCalls = 20
				cfg.ToolSchemaMode = "eager"
				if scenario == "legacy_text" || scenario == "legacy_invalid" {
					cfg.AutonomousMaxIterations = 2
				}
				if scenario == "budget" {
					cfg.RunMaxModelCalls = 1
					if mode == "autonomous" {
						cfg.RunMaxModelCalls = 3
					}
				}
				application, err := New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if err := application.Close(ctx); err != nil {
						t.Error(err)
					}
				}()
				server := httptest.NewServer(application.server.Handler)
				defer server.Close()
				client := &http.Client{Timeout: 30 * time.Second}
				call := func(method, path string, body any, want int, out any) string {
					t.Helper()
					data, _ := json.Marshal(body)
					req, _ := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(data))
					req.Header.Set("Content-Type", "application/json")
					res, err := client.Do(req)
					if err != nil {
						t.Fatal(err)
					}
					defer res.Body.Close()
					b, _ := io.ReadAll(res.Body)
					if res.StatusCode != want {
						t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
					}
					if out != nil {
						if err := json.Unmarshal(b, out); err != nil {
							t.Fatalf("JSON: %v %s", err, b)
						}
					}
					return string(b)
				}
				var agent domain.Agent
				call("POST", "/api/agents", map[string]any{"name": "Clarification fixture", "routing_hints": map[string]any{"capabilities": []string{"report"}, "task_examples": []string{"clarification-gate: prepare a report, audience unspecified"}}, "system_prompt": "Complete the report. Ask for required missing information.", "memory_enabled": false, "retrieval_enabled": false, "tools": []string{}}, 201, &agent)
				request := map[string]any{"agent_id": agent.ID, "mode": mode, "message": task}
				if contracted {
					spec := domain.VerifierSpec{ID: "report", Type: domain.VerifierTextConstraints, Required: true, Config: map[string]any{"required_phrases": []string{"Engineering leads"}}}
					contract := &domain.CompletionContract{Policy: domain.VerificationPolicy{Mode: domain.VerificationAllMustPass, MaxAttempts: 1, OnExhausted: domain.VerificationFailRun}, Verifiers: []domain.VerifierSpec{spec}}
					if structured {
						contract.Verifiers[0].Type = domain.VerifierJSONSchema
						contract.Verifiers[0].Config = map[string]any{"schema": map[string]any{"type": "object"}}
						contract.Output = &domain.StructuredOutputConfig{Mode: "local", VerifierID: spec.ID}
						if ordinary {
							contract.Output.Mode = "strict"
							contract.Verifiers[0].Config = map[string]any{"schema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"summary": map[string]any{"type": "string"}, "facts": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}, "required": []string{"summary", "facts"}}}
						}
					}
					request["completion_contract"] = contract
				}
				payload := call("POST", "/api/chat", request, 200, nil)
				if ordinary {
					done := clarificationDoneJSON(t, payload)
					if done["status"] != "completed" || done["verification_status"] != "passed" || done["result_url"] != "/api/runs/"+done["run_id"].(string)+"/result" || done["title"] != "Fixture conversation" {
						t.Fatalf("ordinary completion protocol: %s", payload)
					}
					call("GET", done["result_url"].(string), nil, 200, nil)
					t.Logf("ref1101-02 ordinary SSE: %s", payload)
					return
				}
				if scenario == "legacy_invalid" {
					runs, err := application.store.ListRuns()
					if err != nil || len(runs) != 1 || runs[0].Status != domain.RunFailed {
						t.Fatalf("invalid question accepted: %+v %v %s", runs, err, payload)
					}
					steps, err := application.store.ListCollaborationSteps(runs[0].ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, step := range steps {
						if step.Status == domain.CollaborationStepRunning {
							t.Fatalf("rejected question left open stage: %+v", step)
						}
					}
					return
				}
				if strings.Contains(payload, "event: error") || !strings.Contains(payload, "waiting_for_user") {
					t.Fatalf("not paused: %s", payload)
				}
				runs, err := application.store.ListRuns()
				if err != nil || len(runs) != 1 {
					t.Fatalf("runs: %+v %v", runs, err)
				}
				run := runs[0]
				if run.Status != domain.RunWaitingForUser || run.ExecutionStartedAt != nil {
					t.Fatalf("not paused: %+v", run)
				}
				snapshot, _ := json.Marshal(run.RuntimeSnapshot)
				var questions []domain.Clarification
				path := "/api/runs/" + run.ID + "/clarifications"
				call("GET", path, nil, 200, &questions)
				if len(questions) != 1 || questions[0].Status != "pending" {
					t.Fatalf("questions: %+v", questions)
				}
				q := questions[0]
				if scenario == "cancel" || scenario == "withdraw" {
					if scenario == "cancel" {
						call("POST", "/api/runs/"+run.ID+"/cancel", nil, 200, nil)
					} else {
						call("DELETE", path+"/"+q.ID, map[string]int64{"version": q.Version}, 200, nil)
					}
					call("POST", path+"/"+q.ID+"/answers", domain.ClarificationAnswer{Version: q.Version, IdempotencyKey: "after-cancel", Answers: map[string]string{"audience": "Engineering leads"}}, 409, nil)
					call("POST", path+"/"+q.ID+"/resume", nil, 409, nil)
					replay, _, err := application.store.GetRunReplay(run.ID)
					if err != nil || replay.Run.Status != domain.RunCanceled || len(replay.Projection.InvariantFailures) != 0 {
						t.Fatalf("cancel left inconsistent state: %+v %v", replay, err)
					}
					return
				}
				call("POST", "/api/runs/"+run.ID+"/resume", map[string]string{"user_input": "unbound answer"}, 409, nil)
				call("POST", path+"/"+q.ID+"/answers", map[string]any{"version": q.Version, "idempotency_key": "bad", "answers": map[string]string{"other": "bad"}}, 400, nil)
				// Recreate the production application over the same fixture database before
				// answering: the question/continuation cannot depend on a browser or goroutine.
				server.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err = application.Close(ctx)
				cancel()
				if err != nil {
					t.Fatal(err)
				}
				application, err = New(cfg)
				if err != nil {
					t.Fatal(err)
				}
				server = httptest.NewServer(application.server.Handler)
				defer server.Close()
				call("GET", path, nil, 200, &questions)
				if len(questions) != 1 || questions[0].ID != q.ID {
					t.Fatal("question lost after restart")
				}
				answerField := q.Questions[0].ID
				answer := domain.ClarificationAnswer{Version: q.Version, IdempotencyKey: "answer-once", Answers: map[string]string{answerField: "Engineering leads"}}
				call("POST", path+"/"+q.ID+"/answers", answer, 200, nil)
				call("POST", path+"/"+q.ID+"/answers", answer, 200, nil)
				answer.Answers[answerField] = "Managers"
				call("POST", path+"/"+q.ID+"/answers", answer, 409, nil)
				candidateID := ""

				if scenario == "claim_restart" || scenario == "corrupt" || scenario == "corrupt_identity" || scenario == "completed_restart" || scenario == "unsafe_restart" {
					db, err := sql.Open("pgx", database)
					if err != nil {
						t.Fatal(err)
					}
					defer db.Close()
					if scenario != "corrupt" && scenario != "corrupt_identity" {
						target := application.store.(*observableStore).Store.(clarification.Store)
						if _, err = target.ClaimClarification(t.Context(), identity.SuperUserID, run.WorkspaceID, run.ID, q.ID); err != nil {
							t.Fatal(err)
						}
						if scenario == "completed_restart" {
							output := "Report for Engineering leads."
							if structured {
								output = `{"summary":"Report for Engineering leads. [S99] [W99]"}`
							}
							// Seed only the durable completion boundary lost between execution and
							// application completion; subsequent recovery is real production code.
							turnID := "fixture-completed-turn"
							for _, kind := range []domain.RunEventType{domain.EventTurnStarted, domain.EventTurnCompleted} {
								payload := map[string]any{"agent_id": agent.ID}
								if kind == domain.EventTurnCompleted {
									payload["output"] = output
									payload["stop_reason"] = "completed"
								}
								if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, StageID: q.StageID, TurnID: turnID, Type: kind, Payload: payload}); err != nil {
									t.Fatal(err)
								}
							}
							if q.StageID != "" {
								step, err := application.store.UpdateCollaborationStep(q.StageID, domain.CollaborationStepCompleted, output, "")
								if err != nil {
									t.Fatal(err)
								}
								if _, err = checkpoint.NewInternalProvider(application.store).RecordStageTransition(t.Context(), step, domain.EventStageCompleted); err != nil {
									t.Fatal(err)
								}
							}
							if mode == "single" {
								candidate, err := application.store.AddMessageWithSources(run.ConversationID, "assistant", output, nil, nil)
								if err != nil {
									t.Fatal(err)
								}
								candidateID = candidate.ID
								binding := map[string]any{"message_id": candidate.ID, "protocol_version": domain.RAGCitationProtocolVersion}
								if bindingFault == "missing_primary" {
									binding["protocol_version"] = domain.WebCitationProtocolVersion
								}
								if contracted || bindingFault == "malformed_hash" {
									events, err := application.store.ListRunEvents(run.ID)
									if err != nil {
										t.Fatal(err)
									}
									question, err := clarification.AddAnswers(task, events)
									if err != nil {
										t.Fatal(err)
									}
									data, _ := json.Marshal(struct{ RunID, MessageID, Question, Output string }{run.ID, candidate.ID, question, output})
									binding["verification_question"] = question
									binding["verification_subject_hash"] = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
								}
								if bindingFault == "malformed_hash" {
									binding["verification_subject_hash"] = "sha256:corrupt"
								}
								if structured {
									binding["invalid_source_ids"] = []string{"S99"}
								}
								if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: domain.EventCitationResolved, Payload: binding}); err != nil {
									t.Fatal(err)
								}
								if bindingFault == "invalid_handoff" {
									if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: domain.EventRunResumed, Payload: map[string]any{"kind": "clarification", "clarification": "corrupt persisted handoff"}}); err != nil {
										t.Fatal(err)
									}
								}
								if bindingFault == "completion_write_failure" {
									_, err = db.ExecContext(t.Context(), `CREATE FUNCTION reject_fixture_completion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'completed' THEN RAISE EXCEPTION 'fixture completion write rejected'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER reject_fixture_completion BEFORE UPDATE ON runs FOR EACH ROW EXECUTE FUNCTION reject_fixture_completion();`)
									if err != nil {
										t.Fatal(err)
									}
								}
								if structured {
									if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: domain.EventCitationResolved, Payload: map[string]any{"message_id": candidate.ID, "protocol_version": domain.WebCitationProtocolVersion, "invalid_source_ids": []string{"W99"}}}); err != nil {
										t.Fatal(err)
									}
								}
								if bindingFault == "malformed_latest" || bindingFault == "latest_mismatch" {
									latest := map[string]any{"protocol_version": domain.RAGCitationProtocolVersion}
									if bindingFault == "latest_mismatch" {
										newer, err := application.store.AddMessageWithSources(run.ConversationID, "assistant", "Newer candidate, not the recovered Turn output.", nil, nil)
										if err != nil {
											t.Fatal(err)
										}
										latest["message_id"] = newer.ID
										if contracted {
											question := binding["verification_question"].(string)
											data, _ := json.Marshal(struct{ RunID, MessageID, Question, Output string }{run.ID, newer.ID, question, newer.Content})
											latest["verification_question"] = question
											latest["verification_subject_hash"] = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
										}
									}
									if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: domain.EventCitationResolved, Payload: latest}); err != nil {
										t.Fatal(err)
									}
								}
							}
						}
						if scenario == "unsafe_restart" {
							effect, _, err := application.store.BeginToolEffect(domain.ToolEffectRecord{IdempotencyKey: "post-question-effect", RunID: run.ID, StageID: q.StageID, TurnID: "real-turn", ToolCallID: "writer", ToolName: "fixture_external_writer", RequestHash: "fixture"})
							if err != nil {
								t.Fatal(err)
							}
							if _, err = application.store.CompleteToolEffect(effect.IdempotencyKey, json.RawMessage(`{"written":true}`)); err != nil {
								t.Fatal(err)
							}
						}
						if _, err = db.ExecContext(t.Context(), "UPDATE runs SET heartbeat_at=NOW()-INTERVAL '1 hour' WHERE id=$1", run.ID); err != nil {
							t.Fatal(err)
						}
						server.Close()
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						err = application.Close(ctx)
						cancel()
						if err != nil {
							t.Fatal(err)
						}
						application, err = New(cfg)
						if err != nil {
							t.Fatal(err)
						}
						server = httptest.NewServer(application.server.Handler)
						defer server.Close()
						recovered, _, err := application.store.GetRun(run.ID)
						if err != nil || recovered.Status != domain.RunFailedRecoverable {
							t.Fatalf("repair: %+v %v", recovered, err)
						}
					} else {
						if scenario == "corrupt_identity" {
							if _, err = db.ExecContext(t.Context(), "UPDATE run_clarifications SET tool_call_id='wrong-call' WHERE id=$1", q.ID); err != nil {
								t.Fatal(err)
							}
						} else if _, err = db.ExecContext(t.Context(), "UPDATE run_clarifications SET checkpoint='{}'::jsonb WHERE id=$1", q.ID); err != nil {
							t.Fatal(err)
						}
					}
				}
				beforeResume, err := application.store.ListModelRequestRecords(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				resumePath := path + "/" + q.ID + "/resume"
				var resumeBody any
				if generic {
					resumePath = "/api/runs/" + run.ID + "/resume"
					resumeBody = map[string]string{"user_input": ""}
				}
				payload = call("POST", resumePath, resumeBody, 200, nil)
				if bindingFault != "" {
					current, _, err := application.store.GetRun(run.ID)
					if err != nil || current.Status != domain.RunFailed || !strings.Contains(payload, "event: error") || strings.Contains(payload, "event: done") {
						t.Fatalf("invalid candidate binding did not fail closed: %+v %v %s", current, err, payload)
					}
					messages, err := application.store.ListMessages(run.ConversationID)
					wantCandidates := 1
					if bindingFault == "latest_mismatch" {
						wantCandidates = 2
					}
					candidates := 0
					for _, message := range messages {
						if message.Role == "assistant" {
							candidates++
						}
					}
					if err != nil || candidates != wantCandidates {
						t.Fatalf("invalid binding resaved output: candidates=%d %v", candidates, err)
					}
					evidence, err := application.store.ListVerificationEvidence(run.ID)
					if err != nil || len(evidence) != 0 {
						t.Fatalf("invalid binding reached verification: %+v %v", evidence, err)
					}
					items, err := application.store.ListRunEvents(run.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, item := range items {
						if item.Type == domain.EventRunCompleted {
							t.Fatal("invalid binding completed Run")
						}
					}
					t.Logf("ref1101-02 binding fault=%s contracted=%t run=%s original_message=%s status=%s candidates=%d SSE=%s", bindingFault, contracted, run.ID, candidateID, current.Status, candidates, payload)
					return
				}
				if scenario == "unsafe_restart" {
					unchanged, _, err := application.store.GetRun(run.ID)
					if err != nil || unchanged.Status != domain.RunFailedRecoverable || !strings.Contains(payload, "event: error") {
						t.Fatalf("unsafe retry admitted: %+v %v %s", unchanged, err, payload)
					}
					return
				}
				if scenario == "budget" || scenario == "corrupt" || scenario == "corrupt_identity" {
					if scenario == "corrupt_identity" {
						after, err := application.store.ListModelRequestRecords(run.ID)
						if err != nil || len(after) != len(beforeResume) || !strings.Contains(payload, "stage checkpoint does not match") {
							t.Fatalf("corrupt identity reached model: %d -> %d %v %s", len(beforeResume), len(after), err, payload)
						}
					}
					stopped, _, err := application.store.GetRun(run.ID)
					if err != nil || stopped.Status != domain.RunFailed {
						t.Fatalf("must fail closed: %+v %v %s", stopped, err, payload)
					}
					items, err := application.store.ListCollaborationSteps(run.ID)
					if err != nil {
						t.Fatal(err)
					}
					for _, s := range items {
						if s.Status == domain.CollaborationStepRunning {
							t.Fatalf("orphan stage: %+v", s)
						}
					}
					call("GET", path, nil, 200, &questions)
					if len(questions) != 1 || questions[0].Answers["audience"] != "Engineering leads" {
						t.Fatal("saved answer lost on execution failure")
					}
					return
				}
				if strings.Contains(payload, "event: error") {
					t.Fatalf("resume error: %s", payload)
				}
				if mode == "multi_agent" {
					payload = call("POST", "/api/runs/"+run.ID+"/continue", map[string]any{"plan": "Execute the audience-specific report"}, 200, nil)
					if strings.Contains(payload, "event: error") {
						t.Fatalf("continue: %s", payload)
					}
				}
				final, _, err := application.store.GetRun(run.ID)
				wantStatus := domain.RunCompleted
				if structured && scenario == "happy" {
					wantStatus = domain.RunFailed // The fixture's resumed prose is not JSON.
				}
				if err != nil || final.Status != wantStatus {
					t.Fatalf("not completed: %+v %v", final, err)
				}
				done := clarificationDoneJSON(t, payload)
				if done["run_id"] != run.ID || done["conversation_id"] != run.ConversationID || done["agent_id"] != final.AgentID || done["status"] != string(final.Status) || done["message_id"] == nil || done["title"] != nil {
					t.Fatalf("clarification completion protocol: %s", payload)
				}
				if candidateID != "" && done["message_id"] != candidateID {
					t.Fatalf("replaced durable candidate %s: %s", candidateID, payload)
				}
				if structured {
					if done["result_url"] != "/api/runs/"+run.ID+"/result" {
						t.Fatalf("missing structured result_url: %s", payload)
					}
					if candidateID != "" {
						if fmt.Sprint(done["invalid_citation_ids"]) != "[S99]" || fmt.Sprint(done["invalid_web_citation_ids"]) != "[W99]" {
							t.Fatalf("lost candidate citation protocol: %s", payload)
						}
						call("GET", done["result_url"].(string), nil, 200, nil)
					}
				}
				if contracted {
					items, err := application.store.ListRunEvents(run.ID)
					if err != nil {
						t.Fatal(err)
					}
					verified := int64(0)
					completed := 0
					for _, item := range items {
						if item.Type == domain.EventVerificationPassed {
							verified = item.Sequence
						}
						if item.Type == domain.EventRunCompleted {
							completed++
							if verified == 0 || verified >= item.Sequence {
								t.Fatal("completed before verification")
							}
						}
					}
					wantVerification := "passed"
					if wantStatus == domain.RunFailed {
						wantVerification = "failed"
					}
					if done["verification_status"] != wantVerification || (wantStatus == domain.RunFailed && completed != 0) || (wantStatus == domain.RunCompleted && completed != 1) {
						t.Fatalf("verification settlement: %v completions=%d", done, completed)
					}
				}
				call("POST", resumePath, resumeBody, 409, nil)
				frozen, _ := json.Marshal(final.RuntimeSnapshot)
				if !bytes.Equal(snapshot, frozen) {
					t.Fatal("resume replaced Snapshot")
				}
				call("GET", path, nil, 200, &questions)
				if questions[0].Status != "applied" || questions[0].AppliedAt == nil {
					t.Fatal("missing application receipt")
				}
				if body := call("GET", path, nil, 200, nil); strings.Contains(body, "CLARIFICATION_PROTOCOL_CANARY") || strings.Contains(body, "checkpoint") {
					t.Fatal("protocol exposed")
				}
				replay, _, err := application.store.GetRunReplay(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				records, err := application.store.ListModelRequestRecords(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				foundCapture := false
				foundManifest := false
				for _, record := range records {
					if strings.Contains(record.Capture.Content, "Engineering leads") {
						foundCapture = true
					}
				}
				for _, item := range replay.RunEvents {
					if item.Type == domain.EventContextAssembled {
						data, _ := json.Marshal(item.Payload)
						if strings.Contains(string(data), "tool_result") && strings.Contains(string(data), q.ToolCallID) && q.ToolCallID != "" {
							foundManifest = true
						}
					}
				}
				if scenario != "completed_restart" && (!foundCapture || scenario != "legacy_text" && !foundManifest) {
					t.Fatalf("answer absent from actual capture/manifest: capture=%t manifest=%t", foundCapture, foundManifest)
				}
				if len(replay.Projection.InvariantFailures) != 0 {
					t.Fatalf("invariants: %+v", replay.Projection.InvariantFailures)
				}
				messages, err := application.store.ListMessages(run.ConversationID)
				if err != nil {
					t.Fatal(err)
				}
				answers := 0
				assistantCount := 0
				for _, m := range messages {
					if m.ID == q.ID {
						answers++
					}
					if m.Role == "assistant" {
						assistantCount++
					}
					if m.Role == "assistant" && !strings.Contains(m.Content, "Engineering leads") {
						t.Fatalf("answer omitted audience: %s", m.Content)
					}
				}
				if assistantCount != 1 {
					t.Fatalf("duplicated recovered candidate: %d", assistantCount)
				}
				if answers != 1 {
					t.Fatalf("durable answer count %d", answers)
				}
				wire := httptest.NewRecorder()
				fixture.contracts(wire, nil)
				if strings.Contains(wire.Body.String(), "clarification continuation lost") {
					t.Fatal(wire.Body.String())
				}
				evidence := map[string]any{"schema": "durable-clarification-evidence-v1", "mode": mode, "scenario": scenario, "recovery_route": resumePath, "run_id": run.ID, "snapshot": final.RuntimeSnapshot, "completion_contract": final.CompletionContract, "done": done, "input": task, "clarifications": questions, "checks": []string{"real HTTP/runtime/binding/Postgres", "application restart", "same Run/Snapshot", "reasoning and call identity preserved", "idempotent answer", "invariant projection"}, "limitations": []string{"deterministic provider, not live model compliance", "completed_restart seeds the crash boundary and candidate receipt"}}
				encoded, _ := json.Marshal(evidence)
				t.Log(string(encoded))
			})
		}
	}
}

func clarificationDoneJSON(t *testing.T, payload string) map[string]any {
	t.Helper()
	var done map[string]any
	count := 0
	for _, frame := range strings.Split(payload, "\n\n") {
		if !strings.HasPrefix(frame, "event: done\n") {
			continue
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.SplitN(frame, "\n", 2)[1], "data: ")), &done); err != nil {
			t.Fatalf("done JSON: %v %s", err, frame)
		}
		count++
	}
	if count != 1 || done["type"] != "done" {
		t.Fatalf("expected exactly one completion: %s", payload)
	}
	return done
}
