package agent

import "agentflow-platform/apps/api/internal/testsupport/fixturestore"

import (
	"context"
	"errors"
	"sync"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"

	"agentflow-platform/apps/api/internal/agent/turn"
)

func TestIsolatedTurnContextExcludesConversationHistory(t *testing.T) {
	snapshot := testRuntimeSnapshot()
	model := runtimeTurnModel{runtime: &Runtime{}}
	ctx, _ := model.withContextSession(context.Background(), turn.Request{
		History: []domain.Message{{ID: "message-old", Role: "user", Content: "private conversation history"}},
		Input:   "explicit worker task",
		Context: turn.Context{
			Memories: []domain.RetrievedMemory{{Memory: domain.Memory{ID: "memory-1", Content: "explicit memory"}}},
			Chunks: []domain.RetrievedDocumentChunk{{
				Document: domain.Document{ID: "document-1", Title: "Explicit knowledge"},
				Chunk:    domain.DocumentChunk{ID: "chunk-1", Content: "explicit knowledge"},
			}},
		},
	}, &snapshot, true)
	pack, err := contextassembly.Assemble(ctx, contextassembly.Request{Model: "test", Messages: []contextassembly.Message{
		{Source: contextassembly.SourceSystem, Role: "system", Content: "worker instructions"},
		{Source: contextassembly.SourceCurrentInput, Role: "user", Content: "explicit worker task"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for _, entry := range pack.Manifest.Entries {
		if entry.Selected {
			sources[entry.Source] = true
		}
	}
	if sources[contextassembly.SourceHistory] {
		t.Fatal("isolated stage included conversation history")
	}
	if !sources[contextassembly.SourceMemory] || !sources[contextassembly.SourceKnowledge] {
		t.Fatalf("isolated stage dropped explicit retrieval context: %#v", sources)
	}
}

func TestRuntimeTurnModelRetriesTextOverflowOnlyAfterGenerationAdvances(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, _ := fixtureStore.CreateConversation("overflow recovery")
	config := overflowCompactionConfig()
	client := &overflowRecoveryClient{}
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly = config
	setOverflowModelSnapshot(t, &snapshot, client, config)
	run, _ := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	for index := 0; index < 8; index++ {
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		_, _ = fixtureStore.AddMessage(conversation.ID, role, "compactable historical context")
	}
	history, _ := fixtureStore.ListMessages(conversation.ID)
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: client, ContextAssembly: config})
	result, err := (runtimeTurnModel{runtime: runtime}).Execute(context.Background(), turn.Request{
		RunID: run.ID, TurnID: "turn-overflow-1", ConversationID: conversation.ID,
		Agent: domain.Agent{ID: "agent_planner", SystemPrompt: "help", Executor: domain.DefaultAgentExecutor},
		Role:  "primary", SystemPrompt: "help", History: history, Input: "continue",
		ModelMode: turn.ModelModeText,
	}, func(turn.ModelEvent) {})
	if err != nil || result.Output != "recovered answer" || client.preparedCalls != 2 || client.summaryCalls != 1 {
		t.Fatalf("overflow recovery failed: result=%#v prepared=%d summaries=%d err=%v", result, client.preparedCalls, client.summaryCalls, err)
	}
	latest, ok, err := fixtureStore.GetLatestContextCompaction(conversation.ID)
	if err != nil || !ok || latest.Generation != 1 || latest.Trigger != contextassembly.CompactionTriggerOverflow {
		t.Fatalf("overflow did not advance compaction surface: ok=%v item=%#v err=%v", ok, latest, err)
	}
}

func TestRuntimeTurnModelDoesNotRetryOverflowWhenCompactionFails(t *testing.T) {
	fixtureStore := fixturestore.New()

	conversation, _ := fixtureStore.CreateConversation("overflow failure")
	config := overflowCompactionConfig()
	client := &overflowRecoveryClient{summaryErr: context.DeadlineExceeded}
	snapshot := testRuntimeSnapshot()
	snapshot.ContextAssembly = config
	setOverflowModelSnapshot(t, &snapshot, client, config)
	run, _ := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	for index := 0; index < 6; index++ {
		_, _ = fixtureStore.AddMessage(conversation.ID, "user", "compactable historical context")
	}
	history, _ := fixtureStore.ListMessages(conversation.ID)
	runtime := NewRuntime(RuntimeOptions{Store: fixtureStore, ModelClient: client, ContextAssembly: config})
	_, err := (runtimeTurnModel{runtime: runtime}).Execute(context.Background(), turn.Request{
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
	makeRuntime := func(t *testing.T, summaryErr error) (*Runtime, *fixturestore.Store, domain.Run, provider.Client) {
		t.Helper()
		fixtureStore := fixturestore.New()

		conversation, _ := fixtureStore.CreateConversation("best effort compaction")
		config := overflowCompactionConfig()
		config.ContextWindowTokens = 240
		config.OutputReserveTokens = 20
		config.SafetyMarginTokens = 20
		config.HistoryMaxTokens = 160
		config.CompactionSoftThreshold = 0.25
		config.CompactionHardThreshold = 0.30
		client := &overflowRecoveryClient{summaryErr: summaryErr}
		snapshot := testRuntimeSnapshot()
		snapshot.ContextAssembly = config
		setOverflowModelSnapshot(t, &snapshot, client, config)
		run, _ := fixtureStore.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
		for index := 0; index < 8; index++ {
			_, _ = fixtureStore.AddMessage(conversation.ID, "user", "compactable context repeated several times")
		}
		runtime := NewRuntime(RuntimeOptions{
			Store: fixtureStore, ModelClient: client, ContextAssembly: config,
		})
		return runtime, fixtureStore, run, client
	}

	successRuntime, _, successRun, successClient := makeRuntime(t, nil)
	if compaction := successRuntime.compactContextBestEffort(context.Background(), successRun.ID, successRun.ConversationID, successRun.RuntimeSnapshot, contextassembly.CompactionTriggerHard, "turn-success", successClient); compaction == nil || compaction.Generation != 1 {
		t.Fatalf("best-effort success did not return committed generation: %#v", compaction)
	}
	failureRuntime, _, failureRun, failureClient := makeRuntime(t, context.DeadlineExceeded)
	if compaction := failureRuntime.compactContextBestEffort(context.Background(), failureRun.ID, failureRun.ConversationID, failureRun.RuntimeSnapshot, contextassembly.CompactionTriggerHard, "turn-failure", failureClient); compaction != nil {
		t.Fatalf("best-effort failure should be suppressed: %#v", compaction)
	}
	if compaction, err := (*Runtime)(nil).compactContext(context.Background(), "run", "conversation", nil, contextassembly.CompactionTriggerHard, "", nil); err != nil || compaction != nil {
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

func (c *overflowRecoveryClient) RuntimeIdentity() provider.RuntimeIdentity {
	return provider.RuntimeIdentity{Provider: "test", BaseURL: "https://model.test/v1", Model: "test-model"}
}

func (c *overflowRecoveryClient) WithRuntimeIdentity(provider.RuntimeIdentity) provider.Client {
	return c
}

func setOverflowModelSnapshot(t *testing.T, snapshot *domain.RuntimeSnapshot, client provider.Client, config domain.ContextAssemblyConfig) {
	t.Helper()
	runtime := NewRuntime(RuntimeOptions{ModelClient: client, ContextAssembly: config})
	modelRouting, err := runtime.captureModelRoutingSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.ModelRouting = modelRouting
}

func (c *overflowRecoveryClient) PrepareAgentChat(context.Context, provider.ChatRequest) (provider.PreparedChat, error) {
	return provider.PreparedChat{}, errors.New("unexpected streaming turn")
}

func (c *overflowRecoveryClient) PrepareFollowup(context.Context, []provider.Message) (provider.PreparedChat, error) {
	return provider.PreparedChat{}, errors.New("unexpected tool follow-up")
}

func (c *overflowRecoveryClient) SelectTools(context.Context, provider.PreparedChat, []map[string]any, provider.ChatTrace) (provider.ChatChoice, error) {
	return provider.ChatChoice{}, errors.New("unexpected tool selection")
}

func (c *overflowRecoveryClient) StreamAnswer(context.Context, provider.PreparedChat, provider.ChatStreamKind, provider.ChatTrace, chan<- provider.StreamEvent) (bool, error) {
	return false, errors.New("unexpected streamed answer")
}

func (c *overflowRecoveryClient) CompleteTextDetailed(context.Context, string, string) (provider.TextCompletion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.summaryCalls++
	if c.summaryErr != nil {
		return provider.TextCompletion{}, c.summaryErr
	}
	return provider.TextCompletion{Text: "## Goal\nPreserve context\n## Superseded Instructions\nNone", Model: "summary-model"}, nil
}

func (c *overflowRecoveryClient) PrepareText(context.Context, string, string) (provider.PreparedText, error) {
	return provider.PreparedText{Manifest: domain.ContextManifest{ID: "manifest", ModelCallID: "call", OutputReserveTokens: 100}}, nil
}

func (c *overflowRecoveryClient) CompletePreparedText(context.Context, provider.PreparedText) (provider.TextCompletion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.preparedCalls++
	if c.preparedCalls == 1 {
		return provider.TextCompletion{}, failure.New(failure.Definition{
			Message: "maximum context length exceeded",
			Info:    failure.Info{Code: "context_length_exceeded", Source: "model_provider", Category: failure.CategoryValidation},
		})
	}
	return provider.TextCompletion{Text: "recovered answer", Model: "test-model"}, nil
}

func (c *overflowRecoveryClient) EmbedText(context.Context, string) (provider.Embedding, error) {
	return provider.Embedding{}, nil
}
