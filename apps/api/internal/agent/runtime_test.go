package agent

import (
	"context"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestRetrieveContextRecordsReplayRetrievalEvent(t *testing.T) {
	ctx := context.Background()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: client})

	conversation, err := fileStore.CreateConversation("Demo retrieval")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	memoryText := "The portfolio demo uses pgvector memory retrieval."
	memoryEmbedding, err := client.EmbedText(ctx, memoryText)
	if err != nil {
		t.Fatalf("embed memory: %v", err)
	}
	if _, err := fileStore.CreateMemory(domain.Memory{
		Kind:    "note",
		Content: memoryText,
	}, domain.MemoryEmbedding{
		Provider:   memoryEmbedding.Provider,
		Model:      memoryEmbedding.Model,
		Dimensions: memoryEmbedding.Dimensions,
		Embedding:  memoryEmbedding.Vector,
	}); err != nil {
		t.Fatalf("create memory: %v", err)
	}

	chunkText := "Replay should show the retrieved knowledge chunk used by the answer."
	chunkEmbedding, err := client.EmbedText(ctx, chunkText)
	if err != nil {
		t.Fatalf("embed chunk: %v", err)
	}
	if _, err := fileStore.CreateDocument(domain.Document{
		Title:      "Demo Knowledge",
		SourceType: "text",
		Content:    chunkText,
		Metadata:   map[string]any{"format": "text"},
	}, []domain.DocumentChunk{{
		Content:    chunkText,
		TokenCount: 9,
		Metadata:   map[string]any{"chunk_type": "paragraph"},
	}}, []domain.DocumentChunkEmbedding{{
		Provider:   chunkEmbedding.Provider,
		Model:      chunkEmbedding.Model,
		Dimensions: chunkEmbedding.Dimensions,
		Embedding:  chunkEmbedding.Vector,
	}}); err != nil {
		t.Fatalf("create document: %v", err)
	}

	memories, chunks := runtime.retrieveContext(ctx, run.ID, "pgvector memory retrieval and replay knowledge chunk", true, true, map[string]any{
		"executor":  ExecutorNative,
		"framework": "agentflow-native",
	})
	if len(memories) == 0 {
		t.Fatal("expected retrieved memories")
	}
	if len(chunks) == 0 {
		t.Fatal("expected retrieved document chunks")
	}
	if chunks[0].VectorRank == 0 || chunks[0].LexicalRank == 0 || chunks[0].RRFScore <= 0 || chunks[0].FusionRank == 0 || chunks[0].RerankRank == 0 || chunks[0].Confidence == "" {
		t.Fatalf("expected runtime retrieval to use the shared rerank and gate pipeline, got %#v", chunks[0])
	}

	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	for _, event := range replay.RunEvents {
		if event.Type != domain.EventRetrievalCompleted {
			continue
		}
		if event.Payload["memory_count"] != len(memories) {
			t.Fatalf("expected memory_count %d, got %#v", len(memories), event.Payload["memory_count"])
		}
		if event.Payload["chunk_count"] != len(chunks) {
			t.Fatalf("expected chunk_count %d, got %#v", len(chunks), event.Payload["chunk_count"])
		}
		if _, ok := event.Payload["retrieved_memories"]; !ok {
			t.Fatal("expected retrieved memories in retrieval payload")
		}
		if _, ok := event.Payload["retrieved_chunks"]; !ok {
			t.Fatal("expected retrieved chunks in retrieval payload")
		}
		fusion, ok := event.Payload["fusion"].(domain.FusionInfo)
		if !ok || fusion.Algorithm != "rrf" || fusion.RankConstant != 60 {
			t.Fatalf("expected active fusion configuration in retrieval trace, got %#v", event.Payload["fusion"])
		}
		security, ok := event.Payload["knowledge_security"].(domain.KnowledgeSecurityInfo)
		if !ok || security.PolicyVersion != domain.RAGPromptGuardPolicyVersion || !security.UntrustedContext || security.CheckedCandidates == 0 {
			t.Fatalf("expected knowledge security summary in retrieval trace, got %#v", event.Payload["knowledge_security"])
		}
		retrieved, ok := event.Payload["retrieved_chunks"].([]map[string]any)
		if !ok || len(retrieved) == 0 || retrieved[0]["lexical_rank"] == nil || retrieved[0]["rrf_score"] == nil || retrieved[0]["fusion_rank"] == nil || retrieved[0]["rerank_rank"] == nil || retrieved[0]["confidence"] == nil {
			t.Fatalf("expected shared pipeline ranks in retrieval trace, got %#v", event.Payload["retrieved_chunks"])
		}
		return
	}
	t.Fatal("expected retrieval trace event")
}

func TestRetrieveContextRespectsDisabledAgentConfig(t *testing.T) {
	ctx := context.Background()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: client})

	conversation, err := fileStore.CreateConversation("Disabled retrieval")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	memories, chunks := runtime.retrieveContext(ctx, run.ID, "pgvector memory retrieval and replay knowledge chunk", false, false, map[string]any{
		"agent_id":          "agent_planner",
		"agent_name":        "Planner",
		"executor":          ExecutorNative,
		"framework":         "agentflow-native",
		"memory_enabled":    false,
		"retrieval_enabled": false,
	})
	if len(memories) != 0 || len(chunks) != 0 {
		t.Fatalf("expected no retrieved context when disabled, got memories=%d chunks=%d", len(memories), len(chunks))
	}

	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	for _, event := range replay.RunEvents {
		if event.Type != domain.EventRetrievalCompleted {
			continue
		}
		if event.Payload["memory_enabled"] != false || event.Payload["retrieval_enabled"] != false {
			t.Fatalf("expected disabled config in retrieval payload, got %#v", event.Payload)
		}
		if event.Payload["memory_count"] != 0 || event.Payload["chunk_count"] != 0 {
			t.Fatalf("expected zero retrieval counts, got %#v", event.Payload)
		}
		if _, ok := event.Payload["retrieved_memories"]; ok {
			t.Fatalf("did not expect retrieved memories payload, got %#v", event.Payload)
		}
		if _, ok := event.Payload["retrieved_chunks"]; ok {
			t.Fatalf("did not expect retrieved chunks payload, got %#v", event.Payload)
		}
		return
	}
	t.Fatal("expected retrieval trace event")
}

func TestRetrieveContextTruncatesEmbeddingQuery(t *testing.T) {
	ctx := context.Background()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: newLocalFallbackOpenAIClientForTest()})

	conversation, err := fileStore.CreateConversation("Long retrieval query")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	query := strings.Repeat("retrieval query ", 300)
	runtime.retrieveContext(ctx, run.ID, query, true, true, map[string]any{
		"executor": ExecutorNative,
	})

	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	for _, event := range replay.RunEvents {
		if event.Type != domain.EventRetrievalCompleted {
			continue
		}
		if event.Payload["embedding_query_chars"] != 3000 {
			t.Fatalf("expected embedding query to be truncated to 3000 chars, got %#v", event.Payload["embedding_query_chars"])
		}
		if event.Payload["embedding_query_original_chars"] != len(query) {
			t.Fatalf("expected original query chars %d, got %#v", len(query), event.Payload["embedding_query_original_chars"])
		}
		if event.Payload["embedding_query_truncated"] != true {
			t.Fatalf("expected embedding query truncated flag, got %#v", event.Payload["embedding_query_truncated"])
		}
		return
	}
	t.Fatal("expected retrieval trace event")
}

func TestStreamChatWithLangChainGoExecutorRecordsFrameworkMetadata(t *testing.T) {
	ctx := context.Background()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	runtime := NewRuntime(RuntimeOptions{Store: fileStore, ModelClient: client})

	conversation, err := fileStore.CreateConversation("LangChainGo executor")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	agent, ok, err := fileStore.GetAgent("agent_planner")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if !ok {
		t.Fatal("expected default agent")
	}
	agent.Executor = ExecutorLangChainGo

	memoryText := "LangChainGo executor should reuse AgentFlow retrieved memory."
	memoryEmbedding, err := client.EmbedText(ctx, memoryText)
	if err != nil {
		t.Fatalf("embed memory: %v", err)
	}
	if _, err := fileStore.CreateMemory(domain.Memory{
		Kind:    "note",
		Content: memoryText,
	}, domain.MemoryEmbedding{
		Provider:   memoryEmbedding.Provider,
		Model:      memoryEmbedding.Model,
		Dimensions: memoryEmbedding.Dimensions,
		Embedding:  memoryEmbedding.Vector,
	}); err != nil {
		t.Fatalf("create memory: %v", err)
	}

	events, errs := runtime.StreamChat(ctx, PreparedRun{
		Agent: agent,
		Run:   run,
	}, nil, "Use the LangChainGo executor memory.", ExecutorLangChainGo)

	var output string
	for event := range events {
		if event.Type == domain.EventModelDelta {
			if delta, ok := event.Payload["delta"].(string); ok {
				output += delta
			}
		}
	}
	if err := <-errs; err != nil {
		t.Fatalf("stream chat: %v", err)
	}
	if output == "" {
		t.Fatal("expected streamed output")
	}

	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}

	var sawRetrieval bool
	var sawContextManifest bool
	var sawLLMStart bool
	for _, event := range replay.RunEvents {
		if event.Type == domain.EventRetrievalCompleted {
			sawRetrieval = true
			if event.Payload["executor"] != ExecutorLangChainGo {
				t.Fatalf("expected retrieval executor %q, got %#v", ExecutorLangChainGo, event.Payload["executor"])
			}
			if event.Payload["framework"] != frameworkLangChainGo {
				t.Fatalf("expected retrieval framework %q, got %#v", frameworkLangChainGo, event.Payload["framework"])
			}
		}
		if event.Type == domain.EventContextAssembled {
			sawContextManifest = true
			manifest, _ := event.Payload["manifest"].(map[string]any)
			if manifest["id"] == "" || manifest["assembler_version"] != "context-assembler-v1" {
				t.Fatalf("unexpected context manifest payload: %#v", event.Payload)
			}
		}
		if event.Type == domain.EventModelStarted {
			sawLLMStart = true
			if event.Payload["executor"] != ExecutorLangChainGo {
				t.Fatalf("expected llm_start executor %q, got %#v", ExecutorLangChainGo, event.Payload["executor"])
			}
			if event.Payload["framework"] != frameworkLangChainGo {
				t.Fatalf("expected llm_start framework %q, got %#v", frameworkLangChainGo, event.Payload["framework"])
			}
			if event.Payload["framework_path"] != "chains.LLMChain" {
				t.Fatalf("expected LangChainGo framework path, got %#v", event.Payload["framework_path"])
			}
			if event.Payload["manifest_id"] == "" {
				t.Fatalf("expected model.started to reference a context manifest: %#v", event.Payload)
			}
		}
	}
	if !sawRetrieval {
		t.Fatal("expected retrieval trace event")
	}
	if !sawLLMStart {
		t.Fatal("expected llm_start trace event")
	}
	if !sawContextManifest {
		t.Fatal("expected context.assembled trace event")
	}
}
