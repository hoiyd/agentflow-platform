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
