package agent

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

func TestRuntimeSnapshotIsSecretFreeAndRestoresFrozenConfiguration(t *testing.T) {
	ctx := context.Background()
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	toolPath := filepath.Join(t.TempDir(), "tools.json")
	if err := tools.SaveConfig(toolPath, tools.DefaultConfig()); err != nil {
		t.Fatalf("save tools config: %v", err)
	}
	manager, err := tools.NewManager(toolPath)
	if err != nil {
		t.Fatalf("new tools manager: %v", err)
	}
	client := openai.NewClientWithTimeoutAndEmbeddingModel(
		"do-not-persist-this-key", "https://user:password@openrouter.ai/api/v1?token=private", "http://localhost:11434/api/embed",
		"test-model-v1", "embedding-v1", 1536, time.Second,
	)
	runtime := NewRuntime(RuntimeOptions{
		Store: fileStore, ModelClient: client, Tools: manager,
		RunBudget: domain.RuntimeRunBudget{MaxModelCalls: 12, MaxRuntimeMS: 90_000, MaxToolCalls: 7},
		ContextAssembly: domain.ContextAssemblyConfig{
			AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 32000, OutputReserveTokens: 2048,
			SafetyMarginTokens: 1024, HistoryMaxTokens: 12000, MemoryMaxTokens: 2000, KnowledgeMaxTokens: 4000,
		},
	})
	agent, err := fileStore.CreateAgent(domain.Agent{
		Name: "Frozen agent", SystemPrompt: "original prompt", Tools: []string{"calculator"},
		MemoryEnabled: true, RetrievalEnabled: true, Executor: ExecutorNative,
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	conversation, err := fileStore.CreateConversation("snapshot test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	prepared, err := runtime.PrepareChatRun(ctx, agent.ID, conversation.ID, ExecutorNative)
	if err != nil {
		t.Fatalf("prepare run: %v", err)
	}

	encoded, err := json.Marshal(prepared.Run.RuntimeSnapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	serialized := string(encoded)
	for _, secret := range []string{"do-not-persist-this-key", "password", "token=private"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("snapshot leaked secret %q: %s", secret, serialized)
		}
	}
	if prepared.Run.RuntimeSnapshot.ContextAssembly.ContextWindowTokens != 32000 {
		t.Fatalf("context assembly config was not frozen: %#v", prepared.Run.RuntimeSnapshot.ContextAssembly)
	}
	if prepared.Run.RuntimeSnapshot.RunBudget == nil || prepared.Run.RuntimeSnapshot.RunBudget.MaxModelCalls != 12 || prepared.Run.RuntimeSnapshot.RunBudget.MaxRuntimeMS != 90_000 {
		t.Fatalf("run budget was not frozen: %#v", prepared.Run.RuntimeSnapshot.RunBudget)
	}
	runtime.runBudget.MaxModelCalls = 99
	if prepared.Run.RuntimeSnapshot.RunBudget.MaxModelCalls != 12 {
		t.Fatal("runtime budget mutation changed the frozen snapshot")
	}
	runtime.contextAssemblyConfig = contextassembly.NormalizeConfig(domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 64000})
	if prepared.Run.RuntimeSnapshot.ContextAssembly.ContextWindowTokens != 32000 {
		t.Fatal("runtime config mutation changed the frozen context assembly config")
	}

	agent.SystemPrompt = "mutated prompt"
	agent.Tools = nil
	if _, err := fileStore.UpdateAgent(agent); err != nil {
		t.Fatalf("update agent: %v", err)
	}
	if _, err := manager.SetEnabled("calculator", false); err != nil {
		t.Fatalf("disable calculator: %v", err)
	}

	restored, err := runtime.restoreRuntime(prepared.Run)
	if err != nil {
		t.Fatalf("restore runtime: %v", err)
	}
	if restored.agent.SystemPrompt != "original prompt" {
		t.Fatalf("expected frozen prompt, got %q", restored.agent.SystemPrompt)
	}
	if _, ok := restored.catalog.Resolve("calculator"); !ok {
		t.Fatal("expected frozen tool to remain available after current config disabled it")
	}
	identity := restored.client.RuntimeIdentity()
	if identity.Model != "test-model-v1" || identity.Provider != "openrouter" {
		t.Fatalf("unexpected restored model identity: %#v", identity)
	}

	prepared.Run.RuntimeSnapshot.Tools[0].Description = "changed tool contract"
	if _, err := runtime.restoreRuntime(prepared.Run); err == nil || !strings.Contains(err.Error(), "no longer matches") {
		t.Fatalf("expected changed tool definition to be rejected, got %v", err)
	}
}

func TestRuntimeSnapshotAcceptsV1ThroughV5AndKeepsPreBudgetRunsBudgetless(t *testing.T) {
	for _, version := range []int{
		domain.LegacyRuntimeSnapshotVersion,
		domain.ContextRuntimeSnapshotVersion,
		domain.CompactionRuntimeSnapshotVersion,
		domain.RunBudgetRuntimeSnapshotVersion,
		domain.CurrentRuntimeSnapshotVersion,
	} {
		snapshot := &domain.RuntimeSnapshot{
			SchemaVersion: version, Mode: ChatModeSingle,
			Agent: domain.RuntimeAgentSnapshot{ID: "agent"}, Model: domain.RuntimeModelSnapshot{Model: "model"},
		}
		if version >= domain.RunBudgetRuntimeSnapshotVersion {
			snapshot.RunBudget = &domain.RuntimeRunBudget{}
		}
		if err := validateRuntimeSnapshot(snapshot); err != nil {
			t.Fatalf("snapshot v%d should remain readable: %v", version, err)
		}
		if version < domain.RunBudgetRuntimeSnapshotVersion && snapshot.RunBudget != nil {
			t.Fatalf("older snapshot v%d unexpectedly inherited a budget", version)
		}
	}
}

func TestEffectiveAutonomousRunBudgetUsesStricterModeProfile(t *testing.T) {
	limits := AutonomousLimits{
		MaxIterations: 5, MaxRuntime: 5 * time.Minute, MaxOutputChars: 1000, MaxToolCalls: 20,
	}
	budget := effectiveAutonomousRunBudget(domain.RuntimeRunBudget{
		MaxRuntimeMS: int64((2 * time.Minute).Milliseconds()), MaxToolCalls: 8,
	}, limits)
	if budget.MaxRuntimeMS != (2*time.Minute).Milliseconds() || budget.MaxToolCalls != 8 {
		t.Fatalf("expected stricter values to remain in Run Budget, got %#v", budget)
	}
}

func TestCaptureAutonomousSnapshotStoresRuntimeAndToolsOnlyInRunBudget(t *testing.T) {
	runtime := &Runtime{
		openAI: openai.NewClient("", "", "test-model"),
		autonomousLimits: AutonomousLimits{
			MaxIterations: 5, MaxRuntime: 5 * time.Minute,
			MaxOutputChars: 1000, MaxToolCalls: 20,
		},
		runBudget: domain.RuntimeRunBudget{
			MaxRuntimeMS: (15 * time.Minute).Milliseconds(), MaxToolCalls: 50,
		},
	}
	snapshot, err := runtime.captureRuntimeSnapshot(ChatModeAutonomous, domain.Agent{ID: "agent"}, nil, "")
	if err != nil {
		t.Fatalf("capture snapshot: %v", err)
	}
	if snapshot.RunBudget == nil || snapshot.RunBudget.MaxRuntimeMS != (5*time.Minute).Milliseconds() || snapshot.RunBudget.MaxToolCalls != 20 {
		t.Fatalf("mode profile was not folded into Run Budget: %#v", snapshot.RunBudget)
	}
	if snapshot.AutonomousLimits == nil || snapshot.AutonomousLimits.MaxRuntimeMS != 0 || snapshot.AutonomousLimits.MaxToolCalls != 0 {
		t.Fatalf("autonomous snapshot retained duplicate resource owners: %#v", snapshot.AutonomousLimits)
	}
}

func TestCurrentAutonomousSnapshotUsesRunBudgetAsSingleResourceOwner(t *testing.T) {
	snapshot := &domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion,
		AutonomousLimits: &domain.RuntimeLimitsSnapshot{
			MaxIterations: 5, MaxOutputChars: 1000,
		},
		RunBudget: &domain.RuntimeRunBudget{
			MaxRuntimeMS: (90 * time.Second).Milliseconds(), MaxToolCalls: 7,
		},
	}
	limits, legacyResourceGuards, err := autonomousLimitsFromSnapshot(snapshot)
	if err != nil || legacyResourceGuards {
		t.Fatalf("current snapshot retained duplicate resource guards: legacy=%t err=%v", legacyResourceGuards, err)
	}
	if limits.MaxRuntime != 90*time.Second || limits.MaxToolCalls != 7 {
		t.Fatalf("progress limits were not projected from Run Budget: %#v", limits)
	}
	if reason := limitStopReason(limits, time.Now().Add(-2*time.Minute), 0, 7, legacyResourceGuards); reason != "" {
		t.Fatalf("current loop duplicated Run Budget enforcement: %q", reason)
	}
}

func TestV4AutonomousSnapshotKeepsItsRuntimeAndToolGuards(t *testing.T) {
	snapshot := &domain.RuntimeSnapshot{
		SchemaVersion: domain.RunBudgetRuntimeSnapshotVersion,
		AutonomousLimits: &domain.RuntimeLimitsSnapshot{
			MaxIterations: 5, MaxRuntimeMS: (3 * time.Minute).Milliseconds(),
			MaxOutputChars: 1000, MaxToolCalls: 12,
		},
		RunBudget: &domain.RuntimeRunBudget{
			MaxRuntimeMS: (10 * time.Minute).Milliseconds(), MaxToolCalls: 40,
		},
	}
	limits, legacyResourceGuards, err := autonomousLimitsFromSnapshot(snapshot)
	if err != nil || !legacyResourceGuards {
		t.Fatalf("legacy resource guards were not preserved: legacy=%t err=%v", legacyResourceGuards, err)
	}
	if limits.MaxRuntime != 3*time.Minute || limits.MaxToolCalls != 12 {
		t.Fatalf("legacy limits changed during restore: %#v", limits)
	}
}

func TestRestoreRuntimeRejectsLegacyRunWithoutSnapshot(t *testing.T) {
	runtime := &Runtime{}
	_, err := runtime.restoreRuntime(domain.Run{ID: "legacy"})
	if !strings.Contains(err.Error(), "run cannot be resumed safely") {
		t.Fatalf("expected explicit legacy run error, got %v", err)
	}
}

func TestClientForRunRejectsMissingSnapshot(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: openai.NewClient("", "", "test")})

	if _, err := runtime.clientForRun("missing"); !errors.Is(err, ErrRuntimeSnapshotUnavailable) {
		t.Fatalf("expected missing snapshot error, got %v", err)
	}
}

func TestClientFromSnapshotRejectsCurrentCredentialForAnotherProvider(t *testing.T) {
	runtime := &Runtime{openAI: openai.NewClient("current-key", "https://api.openai.com/v1", "current-model")}
	snapshot := &domain.RuntimeSnapshot{Model: domain.RuntimeModelSnapshot{
		Provider: "openrouter", BaseURL: "https://openrouter.ai/api/v1", Model: "frozen-model",
	}}

	if _, err := runtime.clientFromSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "credential for frozen provider") {
		t.Fatalf("expected provider credential mismatch, got %v", err)
	}
}
