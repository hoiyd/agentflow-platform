package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/skill"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool"
)

func TestSkillSnapshotPreservesMethodsWithoutExpandingTools(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "trusted-method")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(file, []byte("---\nname: trusted-method\ndescription: Inspect facts\nallowed-tools: dangerous_shell\n---\nORIGINAL_METHOD: keep Tools frozen."), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skill.LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	storage := fixturestore.New()
	client := openai.NewClientWithTimeoutAndEmbeddingModel("fixture-key", "http://localhost:1234/v1", "http://localhost:1234/v1", "fixture-model", "fixture-embedding", 3, time.Second)
	runtime := newRuntimeForTest(RuntimeOptions{Store: storage, Skills: catalog}, client)
	agent, err := storage.CreateAgent(domain.Agent{Name: "Skill Agent", Skills: []string{"trusted-method"}})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversation("skill frozen")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := runtime.PrepareChatRunWithContract(t.Context(), agent.ID, conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Run.RuntimeSnapshot.Skills) != 1 || containsString(prepared.Agent.Tools, "dangerous_shell") {
		t.Fatalf("snapshot=%#v", prepared.Run.RuntimeSnapshot)
	}
	if !containsFrozenTool(prepared.Run.RuntimeSnapshot.Tools, skill.LoadToolName) {
		t.Fatal("activation Binding missing")
	}
	if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	restarted := newRuntimeForTest(RuntimeOptions{Store: storage}, client)
	restored, err := restarted.restoreRuntime(prepared.Run)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(restored.agent.Skills, "trusted-method") {
		t.Fatal("Resume lost bound Skill")
	}
	cloned := store.CloneRuntimeSnapshot(*prepared.Run.RuntimeSnapshot)
	if !strings.Contains(cloned.Skills[0].Instructions, "ORIGINAL_METHOD") {
		t.Fatal("snapshot round-trip lost instructions")
	}
	cloned.Skills[0].Instructions = "tampered"
	if err := validateRuntimeSnapshot(cloned); err == nil {
		t.Fatal("Resume accepted changed Skill content")
	}
	plain, err := runtime.captureRuntimeSnapshot(ChatModeSingle, domain.Agent{ID: "plain"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Skills) != 0 || containsFrozenTool(plain.Tools, skill.LoadToolName) {
		t.Fatal("unbound Agent gained Skills")
	}
}

func TestSkillResumeExecutesFrozenMethodAfterDirectoryRemoved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "resume-method")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: resume-method\ndescription: Recover facts\n---\nFROZEN_RESUME_METHOD: preserve reviewed facts."), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skill.LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	var sawFrozenMethod atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/embeddings") {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"embedding": []float64{1, 0}}}})
			return
		}
		var req struct {
			Messages []provider.Message `json:"messages"`
			Stream   bool               `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		count := 0
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "FROZEN_RESUME_METHOD") {
				count++
			}
			if strings.Contains(m.Content, "CHANGED_METHOD") {
				t.Error("Resume reread mutable package")
			}
		}
		// Only the Worker owns Skill bindings; synthetic Reviewer/Finalizer do not.
		if strings.Contains(req.Messages[0].Content, "trusted_skill_method") {
			t.Error("method gained system authority")
		}
		if strings.Contains(req.Messages[0].Content, "Only advertised frozen Tools") && count != 1 {
			t.Errorf("frozen method count=%d", count)
		}
		if count == 1 {
			sawFrozenMethod.Store(true)
		}
		response := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "Recovered reviewed facts."}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 8, "total_tokens": 38}}
		if req.Stream {
			stream, err := modelstream.Completion(response)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, stream)
		} else {
			_ = json.NewEncoder(w).Encode(response)
		}
	}))
	t.Cleanup(server.Close)
	storage := fixturestore.New()
	agent, _, err := storage.GetAgent("agent_planner")
	if err != nil {
		t.Fatal(err)
	}
	agent.Skills = []string{"resume-method"}
	if _, err := storage.UpdateAgent(agent); err != nil {
		t.Fatal(err)
	}
	client := openai.NewClientWithTimeoutAndEmbeddingModel("fixture-not-a-secret", server.URL, server.URL, "resume-fixture", "embedding-fixture", 2, time.Second)
	runtime := newRuntimeForTest(RuntimeOptions{Store: storage, Skills: catalog, RouterMode: RouterModeQuery}, client)
	conversation, err := storage.CreateConversation("skill recovery")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := runtime.PrepareCollaborationRunWithContract(t.Context(), agent.ID, conversation.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	createCompletedStage(t, runtime, prepared.Run, "planner", agent.ID, "Inspect facts", "Plan")
	createCompletedStage(t, runtime, prepared.Run, "router", agent.ID, "Route", agent.ID)
	frozen, err := runtime.restoreRuntime(prepared.Run)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(frozen.catalog, tool.ExecutorOptions{Tracer: eventpkg.NewToolExecutionTracer(eventpkg.NewRecorder(storage), prepared.Run.ID, "prior-worker")})
	ctx := skill.WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: prepared.Run.ID, ConversationID: conversation.ID, StageID: "prior-worker", TurnID: "prior-turn"}), agent.ID, "")
	result := executor.Execute(ctx, tool.ExecutionRequest{CallID: "prior-load", Tool: skill.LoadToolName, Arguments: json.RawMessage(`{"name":"resume-method"}`)})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if _, err := storage.UpdateRunStatus(prepared.Run.ID, domain.RunFailedRecoverable, "worker lost"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("CHANGED_METHOD"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	agent.Skills = nil
	if _, err := storage.UpdateAgent(agent); err != nil {
		t.Fatal(err)
	}
	restarted := newRuntimeForTest(RuntimeOptions{Store: storage, RouterMode: RouterModeQuery}, client)
	events, errs := restarted.ResumeRecoverableCollaboration(t.Context(), prepared.Run.ID)
	for range events {
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !sawFrozenMethod.Load() {
		t.Fatal("Resume completed without the frozen method in model Context")
	}
	steps, err := storage.ListCollaborationSteps(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	for _, step := range steps {
		if step.Role == "worker" && step.Status == domain.CollaborationStepCompleted {
			completed = true
		}
	}
	if !completed {
		t.Fatal("frozen Skill Worker did not recover")
	}
}
