package agent

import (
	"context"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
	"agentflow-platform/apps/api/internal/turn"
)

func TestRuntimeTurnModelRetriesTextOverflowOnlyAfterGenerationAdvances(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("overflow recovery")
	config := overflowCompactionConfig()
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly = config
	snapshot.Model.Provider = "test"
	snapshot.Model.Model = "test-model"
	run, _ := fileStore.CreateRun("agent_planner", conversation.ID, snapshot)
	for index := 0; index < 8; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		_, _ = fileStore.AddMessage(conversation.ID, role, "compactable historical context")
	}
	history, _ := fileStore.ListMessages(conversation.ID)
	client := &overflowRecoveryClient{}
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: client, ContextAssembly: config})
	result, err := (runtimeTurnModel{runtime: runtime}).Execute(context.Background(), turn.Request{
		RunID: run.ID, TurnID: "turn-overflow-1", ConversationID: conversation.ID,
		Agent: domain.Agent{ID: "agent_planner", SystemPrompt: "help", Executor: domain.DefaultAgentExecutor},
		Role:  "primary", SystemPrompt: "help", History: history, Input: "continue",
		ModelMode: turn.ModelModeText,
	}, func(turn.ModelEvent) {})
	if err != nil || result.Output != "recovered answer" || client.preparedCalls != 2 || client.summaryCalls != 1 {
		t.Fatalf("overflow recovery failed: result=%#v prepared=%d summaries=%d err=%v", result, client.preparedCalls, client.summaryCalls, err)
	}
	latest, ok, err := fileStore.GetLatestContextCompaction(conversation.ID)
	if err != nil || !ok || latest.Generation != 1 || latest.Trigger != contextassembly.CompactionTriggerOverflow {
		t.Fatalf("overflow did not advance compaction surface: ok=%v item=%#v err=%v", ok, latest, err)
	}
}

func TestRuntimeTurnModelDoesNotRetryOverflowWhenCompactionFails(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("overflow failure")
	config := overflowCompactionConfig()
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly = config
	snapshot.Model.Provider = "test"
	snapshot.Model.Model = "test-model"
	run, _ := fileStore.CreateRun("agent_planner", conversation.ID, snapshot)
	for index := 0; index < 6; index++ {
		_, _ = fileStore.AddMessage(conversation.ID, "user", "compactable historical context")
	}
	history, _ := fileStore.ListMessages(conversation.ID)
	client := &overflowRecoveryClient{summaryErr: context.DeadlineExceeded}
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: client, ContextAssembly: config})
	_, err = (runtimeTurnModel{runtime: runtime}).Execute(context.Background(), turn.Request{
		RunID: run.ID, TurnID: "turn-overflow-failed", ConversationID: conversation.ID,
		Agent: domain.Agent{ID: "agent_planner", SystemPrompt: "help", Executor: domain.DefaultAgentExecutor},
		Role:  "primary", SystemPrompt: "help", History: history, Input: "continue",
		ModelMode: turn.ModelModeText,
	}, func(turn.ModelEvent) {})
	if failure.Describe(err).Code != "context_length_exceeded" || client.preparedCalls != 1 || client.summaryCalls != 1 {
		t.Fatalf("failed compaction should preserve original overflow: prepared=%d summaries=%d err=%v", client.preparedCalls, client.summaryCalls, err)
	}
}

func TestCompactContextBestEffortReturnsSuccessAndSuppressesFailure(t *testing.T) {
	makeRuntime := func(t *testing.T, summaryErr error) (*Runtime, *store.FileStore, domain.Run) {
		t.Helper()
		fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
		if err != nil {
			t.Fatal(err)
		}
		conversation, _ := fileStore.CreateConversation("best effort compaction")
		config := overflowCompactionConfig()
		config.ContextWindowTokens = 240
		config.OutputReserveTokens = 20
		config.SafetyMarginTokens = 20
		config.HistoryMaxTokens = 160
		config.CompactionSoftThreshold = 0.25
		config.CompactionHardThreshold = 0.30
		snapshot := testRuntimeSnapshot()
		snapshot.Model.Provider = "test"
		snapshot.Model.Model = "test-model"
		snapshot.ContextAssembly = config
		run, _ := fileStore.CreateRun("agent_planner", conversation.ID, snapshot)
		for index := 0; index < 8; index++ {
			_, _ = fileStore.AddMessage(conversation.ID, "user", "compactable context repeated several times")
		}
		runtime := NewRuntime(RuntimeOptions{
			Store: fileStore, ModelClient: &overflowRecoveryClient{summaryErr: summaryErr}, ContextAssembly: config,
		})
		return runtime, fileStore, run
	}

	successRuntime, _, successRun := makeRuntime(t, nil)
	if compaction := successRuntime.compactContextBestEffort(context.Background(), successRun.ID, successRun.ConversationID, successRun.RuntimeSnapshot, contextassembly.CompactionTriggerHard, "turn-success"); compaction == nil || compaction.Generation != 1 {
		t.Fatalf("best-effort success did not return committed generation: %#v", compaction)
	}
	failureRuntime, _, failureRun := makeRuntime(t, context.DeadlineExceeded)
	if compaction := failureRuntime.compactContextBestEffort(context.Background(), failureRun.ID, failureRun.ConversationID, failureRun.RuntimeSnapshot, contextassembly.CompactionTriggerHard, "turn-failure"); compaction != nil {
		t.Fatalf("best-effort failure should be suppressed: %#v", compaction)
	}
	if compaction, err := (*Runtime)(nil).compactContext(context.Background(), "run", "conversation", nil, contextassembly.CompactionTriggerHard, ""); err != nil || compaction != nil {
		t.Fatalf("nil runtime should be a no-op: compaction=%#v err=%v", compaction, err)
	}
}

func overflowCompactionConfig() domain.ContextAssemblyConfig {
	return domain.ContextAssemblyConfig{
		ContextWindowTokens: 10000, OutputReserveTokens: 500, SafetyMarginTokens: 500,
		HistoryMaxTokens: 5000, MemoryMaxTokens: 100, KnowledgeMaxTokens: 100,
		ToolResultMaxTokens: 100, CompactionMode: contextassembly.CompactionModeAuto,
		CompactionSoftThreshold: 0.70, CompactionHardThreshold: 0.85,
		CompactionRecentTokens: 20, CompactionSummaryMaxTokens: 100, CompactionTimeoutMS: 1000,
	}
}

type overflowRecoveryClient struct {
	mu            sync.Mutex
	preparedCalls int
	summaryCalls  int
	summaryErr    error
}

func (c *overflowRecoveryClient) HasAPIKey() bool { return true }

func (c *overflowRecoveryClient) RuntimeIdentity() modelprovider.RuntimeIdentity {
	return modelprovider.RuntimeIdentity{Provider: "test", Model: "test-model"}
}

func (c *overflowRecoveryClient) WithRuntimeIdentity(modelprovider.RuntimeIdentity) modelprovider.Client {
	return c
}

func (c *overflowRecoveryClient) StreamAgentChatWithToolsTrace(context.Context, string, []domain.Message, string, *tools.Catalog, *eventpkg.Recorder, string, string, []domain.RetrievedMemory, []domain.RetrievedDocumentChunk) (<-chan modelprovider.StreamEvent, <-chan error) {
	events := make(chan modelprovider.StreamEvent)
	errs := make(chan error)
	close(events)
	close(errs)
	return events, errs
}

func (c *overflowRecoveryClient) CompleteTextDetailed(context.Context, string, string) (modelprovider.TextCompletion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.summaryCalls++
	if c.summaryErr != nil {
		return modelprovider.TextCompletion{}, c.summaryErr
	}
	return modelprovider.TextCompletion{Text: "## Goal\nPreserve context\n## Superseded Instructions\nNone", Model: "summary-model"}, nil
}

func (c *overflowRecoveryClient) PrepareText(context.Context, string, string) (modelprovider.PreparedText, error) {
	return modelprovider.PreparedText{Manifest: domain.ContextManifest{ID: "manifest", ModelCallID: "call", OutputReserveTokens: 100}}, nil
}

func (c *overflowRecoveryClient) CompletePreparedText(context.Context, modelprovider.PreparedText) (modelprovider.TextCompletion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.preparedCalls++
	if c.preparedCalls == 1 {
		return modelprovider.TextCompletion{}, failure.New(failure.Definition{
			Message: "maximum context length exceeded",
			Info:    failure.Info{Code: "context_length_exceeded", Source: "model_provider", Category: failure.CategoryValidation},
		})
	}
	return modelprovider.TextCompletion{Text: "recovered answer", Model: "test-model"}, nil
}

func (c *overflowRecoveryClient) EmbedText(context.Context, string) (modelprovider.Embedding, error) {
	return modelprovider.Embedding{}, nil
}
