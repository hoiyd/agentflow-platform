package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
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
func TestProductionDurableClarification(t *testing.T) {
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		for _, scenario := range []string{"happy", "cancel", "withdraw", "claim_restart", "budget", "corrupt", "legacy_text", "completed_restart", "unsafe_restart", "legacy_invalid", "corrupt_identity", "generic_happy", "generic_claim_restart", "generic_completed_restart"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				generic := strings.HasPrefix(scenario, "generic_")
				scenario = strings.TrimPrefix(scenario, "generic_")
				if (scenario == "legacy_text" || scenario == "legacy_invalid") && mode != "autonomous" {
					t.Skip("legacy text ask is an Autonomous protocol")
				}
				task := "clarification-gate: prepare a report, audience unspecified"
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
				routes, _ := json.Marshal(map[string]any{"routes": []any{map[string]any{"id": "clarification-fixture", "model": "fixture-model", "base_url": providerServer.URL, "credential_environment": "BROWSER_FIXTURE_KEY", "request_timeout_seconds": 20, "context_window_tokens": 128000, "max_output_tokens": 8192, "priority": 100, "capabilities": map[string]bool{"tool_calling": true, "streaming": true, "structured_output": true}, "pricing": map[string]string{"source": "fixture"}}}})
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
				payload := call("POST", "/api/chat", map[string]string{"agent_id": agent.ID, "mode": mode, "message": task}, 200, nil)
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
								if _, err = application.store.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: run.ConversationID, Type: domain.EventCitationResolved, Payload: map[string]any{"message_id": candidate.ID}}); err != nil {
									t.Fatal(err)
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
				if err != nil || final.Status != domain.RunCompleted {
					t.Fatalf("not completed: %+v %v", final, err)
				}
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
				evidence := map[string]any{"schema": "durable-clarification-evidence-v1", "mode": mode, "scenario": scenario, "recovery_route": resumePath, "run_id": run.ID, "snapshot": final.RuntimeSnapshot, "clarifications": questions, "checks": []string{"real HTTP/runtime/binding/Postgres", "application restart", "same Run/Snapshot", "reasoning and call identity preserved", "idempotent answer", "invariant projection"}, "limitations": []string{"deterministic provider, not live model compliance"}}
				encoded, _ := json.Marshal(evidence)
				t.Log(string(encoded))
			})
		}
	}
}
