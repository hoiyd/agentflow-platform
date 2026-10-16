package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/planning"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/filesystem"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Inventory: restart must retain the restriction; content/inode/root/grant
// drift must fail; repeated concurrent execution must create exactly one Run.
func TestPlanningPostgresSourceBoundaryAndRestart(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	workspace, err := s.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	root, config := t.TempDir(), filepath.Join(t.TempDir(), "roots.json")
	sourcePath := filepath.Join(root, "report.md")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(sourcePath, "Original project evidence")
	data, _ := json.Marshal(map[string]any{"roots": []filesystem.RootConfig{{ID: "project", WorkspaceID: workspace, Path: root}}})
	write(config, string(data))
	files, err := filesystem.New(config, s)
	if err != nil {
		t.Fatal(err)
	}
	cfg := tool.DefaultConfig()
	for _, b := range files.Bindings() {
		cfg.EnabledTools = append(cfg.EnabledTools, b.Descriptor.Name)
		cfg.SecurityPolicy.Rules = append(cfg.SecurityPolicy.Rules, policy.Rule{ID: b.Descriptor.Name, Tool: b.Descriptor.Name, Action: policy.ActionAllowAndLog, Capability: b.Descriptor.Security})
	}
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := tool.SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	manager, err := tool.NewManager(path, files.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntimeForTest(RuntimeOptions{Store: s, Tools: manager, PlanningSources: files}, nil)
	owned, err := s.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "Project planner", Tools: []string{"file_read", "file_write", "file_patch"}})
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.CreateConversationInWorkspace(workspace, "Planning evidence")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := r.PrepareChatRunWithContract(WithExecutionIntent(t.Context(), domain.ExecutionPlanOnly, nil), owned.ID, conv.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := r.restoreRuntime(prepared.Run)
	if err != nil {
		t.Fatal(err)
	}
	ctx := event.WithScope(t.Context(), event.Scope{RunID: prepared.Run.ID, ConversationID: conv.ID})
	executor := tool.NewExecutor(frozen.catalog, tool.ExecutorOptions{PlanOnly: true, Authorize: r.authorizeTool(prepared.Run.ID, frozen.agent), Tracer: event.NewToolExecutionTracer(event.NewRecorder(s), prepared.Run.ID, "")})
	read := executor.Execute(ctx, tool.ExecutionRequest{RunID: prepared.Run.ID, CallID: "source", Tool: "file_read", Arguments: []byte(`{"root":"project","path":"report.md"}`)})
	if read.Error != nil {
		t.Fatal(read.Error)
	}
	for _, name := range []string{"file_write", "file_patch", "update_task_state", "sandbox_command"} {
		result := executor.Execute(ctx, tool.ExecutionRequest{RunID: prepared.Run.ID, CallID: name, Tool: name, Arguments: []byte(`{}`)})
		if result.Error == nil || result.PolicyDecision == nil || result.PolicyDecision.Reason != "plan_only_denied" {
			t.Fatalf("write escaped: %#v", result)
		}
	}
	items, err := s.ListRunEvents(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planning.Record(prepared.Run, "Inspect", "Proposed report: save a sourced report; check its text", items)
	if err != nil {
		t.Fatal(err)
	}
	item, err := event.NewRunEvent(domain.EventPlanRecorded, event.EventMetadata{RunID: prepared.Run.ID, ConversationID: conv.ID}, event.PlanPayload{RunPlan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRunEvent(item); err != nil {
		t.Fatal(err)
	}
	completed, err := r.CompleteRun(prepared.Run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := planning.Validate(completed, &plan, plan.Hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.ValidatePlanSources(t.Context(), completed, plan.Sources); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"unknown restriction", "in-place upgrade", "mutable snapshot"} {
		copy := store.CloneRun(completed)
		switch scenario {
		case "unknown restriction":
			copy.RuntimeSnapshot.ExecutionConstraint = "unsafe"
		case "in-place upgrade":
			copy.RuntimeSnapshot.PlanOrigin = &domain.PlanOrigin{RunID: completed.ID, Hash: plan.Hash}
		case "mutable snapshot":
			copy.RuntimeSnapshot.Tools[0].Security.SideEffect = policy.SideEffectInternalWrite
		}
		if _, err := r.restoreRuntime(copy); err == nil {
			t.Fatal("invalid planning Snapshot restored: " + scenario)
		}
	}
	for _, constraint := range []string{"unsafe", domain.ExecutionPlanOnly} {
		var origin *domain.PlanOrigin
		if constraint == domain.ExecutionPlanOnly {
			origin = &domain.PlanOrigin{RunID: completed.ID, Hash: plan.Hash}
		}
		if _, err := r.PrepareChatRunWithContract(WithExecutionIntent(t.Context(), constraint, origin), owned.ID, conv.ID, nil); err == nil {
			t.Fatal("unsafe intent persisted")
		}
	}
	for _, scenario := range []string{"root revision", "foreign root", "traversal", "binary replacement"} {
		sources := append([]domain.PlanSource(nil), plan.Sources...)
		switch scenario {
		case "root revision":
			sources[0].RootRevision = "revoked"
		case "foreign root":
			sources[0].Root = "another-workspace-root"
		case "traversal":
			sources[0].Path = "../outside.md"
		case "binary replacement":
			write(sourcePath, "binary\x00data")
		}
		if r.ValidatePlanSources(t.Context(), completed, sources) == nil {
			t.Fatal("unsafe source accepted: " + scenario)
		}
		write(sourcePath, "Original project evidence")
	}
	withoutValidator := newRuntimeForTest(RuntimeOptions{Store: s, Tools: manager}, nil)
	if withoutValidator.ValidatePlanSources(t.Context(), completed, plan.Sources) == nil {
		t.Fatal("missing validator accepted")
	}
	withoutFiles := newRuntimeForTest(RuntimeOptions{Store: s, PlanningSources: files}, nil)
	if withoutFiles.ValidatePlanSources(t.Context(), completed, plan.Sources) == nil {
		t.Fatal("missing file Tool accepted")
	}
	write(sourcePath, "Changed project evidence")
	if r.ValidatePlanSources(t.Context(), completed, plan.Sources) == nil {
		t.Fatal("changed source accepted")
	}
	write(sourcePath, "Original project evidence")
	if err := s.SetWorkspaceToolEnabled(workspace, "file_read", false); err != nil {
		t.Fatal(err)
	}
	if r.ValidatePlanSources(t.Context(), completed, plan.Sources) == nil {
		t.Fatal("revoked grant accepted")
	}
	if err := s.SetWorkspaceToolEnabled(workspace, "file_read", true); err != nil {
		t.Fatal(err)
	}
	write(config, `{"roots":[]}`)
	if r.ValidatePlanSources(t.Context(), completed, plan.Sources) == nil {
		t.Fatal("revoked root accepted")
	}
	write(config, string(data))
	if err := os.Rename(sourcePath, filepath.Join(root, "old.md")); err != nil {
		t.Fatal(err)
	}
	write(sourcePath, "Original project evidence")
	if r.ValidatePlanSources(t.Context(), completed, plan.Sources) == nil {
		t.Fatal("replaced inode accepted")
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "old.md"), sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, ok, err := reopened.GetRun(completed.ID)
	if err != nil || !ok || stored.RuntimeSnapshot.ExecutionConstraint != domain.ExecutionPlanOnly {
		t.Fatalf("restriction lost: %+v %v", stored, err)
	}
	storedEvents, err := reopened.ListRunEvents(stored.ID)
	if err != nil || planning.Read(storedEvents).Hash != plan.Hash {
		t.Fatalf("plan lost: %v", err)
	}
	restartedFiles, err := filesystem.New(config, reopened)
	if err != nil {
		t.Fatal(err)
	}
	restartedManager, err := tool.NewManager(path, restartedFiles.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	restarted := newRuntimeForTest(RuntimeOptions{Store: reopened, Tools: restartedManager, PlanningSources: restartedFiles}, nil)
	if _, err := restarted.restoreRuntime(stored); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ValidatePlanSources(t.Context(), stored, plan.Sources); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := restarted.PrepareChatRunWithContract(WithExecutionIntent(t.Context(), "", &domain.PlanOrigin{RunID: stored.ID, Hash: plan.Hash}), owned.ID, conv.ID, nil)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, planning.ErrAlreadyStarted) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("duplicate execution: success=%d conflict=%d", success, conflict)
	}
	old, _, _ := reopened.GetRun(stored.ID)
	execution, ok, err := reopened.GetRun(planning.ExecutionRunID(stored.ID))
	if err != nil || !ok || execution.RuntimeSnapshot.ExecutionConstraint != "" || old.RuntimeSnapshot.ExecutionConstraint != domain.ExecutionPlanOnly || !execution.RuntimeSnapshot.CreatedAt.After(old.RuntimeSnapshot.CreatedAt) {
		t.Fatalf("execution upgraded old authority: %+v %v", execution, err)
	}
}
