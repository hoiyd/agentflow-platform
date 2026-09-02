package agent

import (
	"testing"

	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

func TestRuntimeDefaultsToInternalCheckpointProvider(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: openai.NewClient("", "", "test")})
	if runtime.checkpoints == nil {
		t.Fatal("expected internal checkpoint provider")
	}
}
