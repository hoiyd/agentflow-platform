package skill

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

func TestSkillActivationFrozenResourcesAndAgentBoundary(t *testing.T) {
	dir := packageDirectory(t, "bounded", "name: bounded\ndescription: Evidence method", "Use evidence; never expand Tool authority.")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	packages, err := catalog.Freeze([]string{"bounded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	storage := fixturestore.New()
	for _, id := range []string{"agent-a", "agent-b", "agent-denied"} {
		if _, err := storage.CreateAgent(domain.Agent{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	conversation, err := storage.CreateConversationInWorkspace("workspace-a", "skills")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Agent: domain.RuntimeAgentSnapshot{ID: "agent-a", Skills: []string{"bounded"}}, CandidateAgents: []domain.RuntimeAgentSnapshot{{ID: "agent-b", Skills: []string{"bounded"}}, {ID: "agent-denied"}}, Skills: packages}
	run, err := storage.CreateRunWithContract("agent-a", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(storage)
	bindings, err := tool.NewCatalog(service.ToolBindings()...)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(bindings, tool.ExecutorOptions{Tracer: eventpkg.NewToolExecutionTracer(eventpkg.NewRecorder(storage), run.ID, "stage-skill")})
	ctx := eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-skill", TurnID: "turn-a"})
	ctx = WithAgent(ctx, "agent-a", "")
	invoke := func(ctx context.Context, call, name, args string) tool.ExecutionResult {
		return executor.Execute(ctx, tool.ExecutionRequest{CallID: call, Tool: name, Arguments: json.RawMessage(args)})
	}
	if result := invoke(ctx, "before", "skill_read", `{"name":"bounded","path":"references/checklist.md"}`); result.Error == nil {
		t.Fatal("resource read before activation accepted")
	}
	if active, err := service.Active(ctx, &snapshot); err != nil || len(active) != 0 {
		t.Fatalf("initial active=%#v err=%v", active, err)
	}
	load := invoke(ctx, "load", "skill_load", `{"name":"bounded"}`)
	if load.Error != nil {
		t.Fatal(load.Error)
	}
	encoded, _ := json.Marshal(load.Result)
	if strings.Contains(string(encoded), "never expand") {
		t.Fatal("load result duplicates instructions instead of Context selection")
	}
	active, err := service.Active(ctx, &snapshot)
	if err != nil || len(active) != 1 || !strings.Contains(active[0].Instructions, "never expand") {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	load = invoke(ctx, "duplicate", "skill_load", `{"name":"bounded"}`)
	if load.Error != nil {
		t.Fatal(load.Error)
	}
	active, err = service.Active(ctx, &snapshot)
	if err != nil || len(active) != 1 {
		t.Fatal("duplicate activation duplicated instructions")
	}
	read := invoke(ctx, "read", "skill_read", `{"name":"bounded","path":"references/checklist.md","limit":12}`)
	if read.Error != nil {
		t.Fatal(read.Error)
	}
	encoded, _ = json.Marshal(read.Result)
	if !strings.Contains(string(encoded), "next_offset") {
		t.Fatal("page cannot be continued")
	}
	for _, agent := range []string{"agent-b", "agent-denied", "unknown"} {
		result := invoke(WithAgent(ctx, agent, ""), "isolation-"+agent, "skill_read", `{"name":"bounded","path":"references/checklist.md"}`)
		if result.Error == nil {
			t.Fatalf("Agent %s inherited activation", agent)
		}
	}
	if result := invoke(WithAgent(ctx, "agent-b", ""), "load-b", "skill_load", `{"name":"bounded"}`); result.Error != nil {
		t.Fatal("second bound Agent could not reuse Skill", result.Error)
	}
	if active, err := NewService(storage).Active(WithAgent(ctx, "agent-a", "/skill:bounded Find facts."), &snapshot); err != nil || len(active) != 1 {
		t.Fatalf("restored active=%#v err=%v", active, err)
	}
	for _, args := range []string{`{"name":"unbound"}`, `{"name":"bounded","workspace_id":"foreign"}`, `{"name":"bounded","agent_id":"agent-b"}`} {
		if result := invoke(ctx, args, "skill_load", args); result.Error == nil {
			t.Fatalf("expanding arguments accepted %s", args)
		}
	}
	if result := invoke(context.Background(), "scope", "skill_load", `{"name":"bounded"}`); result.Error == nil {
		t.Fatal("unscoped invocation accepted")
	}
}

type skillFaultStore struct{ *fixturestore.Store }

func (s skillFaultStore) ListRunEvents(string) ([]domain.RunEvent, error) {
	return nil, errors.New("event store unavailable")
}

func TestSkillExplicitActivationAndFailure(t *testing.T) {
	dir := packageDirectory(t, "bounded", "name: bounded\ndescription: Evidence method", "Keep facts.")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	packages, err := catalog.Freeze([]string{"bounded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Agent: domain.RuntimeAgentSnapshot{ID: "agent", Skills: []string{"bounded"}}, Skills: packages}
	ctx := eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: "run", ConversationID: "conversation"})
	service := NewService(fixturestore.New())
	active, err := service.Active(WithAgent(ctx, "agent", "/skill:bounded Keep facts."), snapshot)
	if err != nil || len(active) != 1 {
		t.Fatalf("explicit=%#v err=%v", active, err)
	}
	if _, err := service.Active(WithAgent(ctx, "agent", "/skill:unknown request"), snapshot); err == nil {
		t.Fatal("explicit unbound Skill accepted")
	}
	if _, err := NewService(skillFaultStore{fixturestore.New()}).Active(WithAgent(ctx, "agent", ""), snapshot); err == nil {
		t.Fatal("store failure ignored")
	}
}

func TestSkillActivationRejectsCorruptOrIncompleteEvidence(t *testing.T) {
	dir := packageDirectory(t, "bounded", "name: bounded\ndescription: useful", "Body")
	catalog, err := LoadDirectories([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	packages, err := catalog.Freeze([]string{"bounded"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Agent: domain.RuntimeAgentSnapshot{ID: "agent", Skills: []string{"bounded"}}, Skills: packages}
	for _, tc := range []struct {
		name      string
		payload   map[string]any
		wantError bool
	}{
		{"failed", map[string]any{"tool_name": LoadToolName, "error": "failed", "result": loadResult{SkillMetadata: Metadata(packages[0]), AgentID: "agent"}}, false},
		{"spilled", map[string]any{"tool_name": LoadToolName, "truncated": true, "result": loadResult{SkillMetadata: Metadata(packages[0]), AgentID: "agent"}}, false},
		{"malformed", map[string]any{"tool_name": LoadToolName, "result": "not JSON metadata"}, false},
		{"foreign", map[string]any{"tool_name": LoadToolName, "result": loadResult{SkillMetadata: Metadata(packages[0]), AgentID: "other"}}, false},
		{"changed", map[string]any{"tool_name": LoadToolName, "result": loadResult{SkillMetadata: domain.SkillMetadata{Name: "bounded", Hash: "changed"}, AgentID: "agent"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := fixturestore.New()
			conversation, err := storage.CreateConversation("activation")
			if err != nil {
				t.Fatal(err)
			}
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, *snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := storage.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventToolCompleted, Payload: tc.payload}); err != nil {
				t.Fatal(err)
			}
			ctx := WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: run.ID, ConversationID: conversation.ID}), "agent", "")
			active, err := NewService(storage).Active(ctx, snapshot)
			if (err != nil) != tc.wantError || len(active) != 0 {
				t.Fatalf("active=%#v err=%v", active, err)
			}
		})
	}
	service := NewService(fixturestore.New())
	ctx, cancel := context.WithCancel(WithAgent(t.Context(), "agent", ""))
	cancel()
	if _, err := service.Active(ctx, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	if _, err := service.runContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("scope ignored cancellation")
	}
	ctx = WithAgent(eventpkg.WithScope(t.Context(), eventpkg.Scope{RunID: "missing", ConversationID: "missing"}), "agent", "")
	if _, err := service.runContext(ctx); err == nil {
		t.Fatal("unpersisted scope accepted")
	}
	if len((*Service)(nil).ToolBindings()) != 0 || len(NewService(nil).ToolBindings()) != 0 {
		t.Fatal("unconfigured service installed tools")
	}
}
