package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
	tracepkg "agentflow-platform/apps/api/internal/trace"
	turnpkg "agentflow-platform/apps/api/internal/turn"
)

type Runtime struct {
	store            store.Store
	openAI           *openai.Client
	tools            *tools.Manager
	trace            *tracepkg.Recorder
	turnEngine       *turnpkg.Engine
	routerMode       string
	autonomousLimits AutonomousLimits
}

type AutonomousLimits struct {
	MaxIterations  int
	MaxRuntime     time.Duration
	MaxOutputChars int
	MaxToolCalls   int
}

type PreparedRun struct {
	Agent    domain.Agent
	Run      domain.Run
	Registry *tools.Registry
}

func NewRuntime(store store.Store, openAI *openai.Client, tools *tools.Manager) *Runtime {
	return NewRuntimeWithRouterMode(store, openAI, tools, RouterModeAuto)
}

func NewRuntimeWithRouterMode(store store.Store, openAI *openai.Client, tools *tools.Manager, routerMode string) *Runtime {
	return NewRuntimeWithRouterModeAndLimits(store, openAI, tools, routerMode, DefaultAutonomousLimits())
}

func NewRuntimeWithRouterModeAndLimits(store store.Store, openAI *openai.Client, tools *tools.Manager, routerMode string, limits AutonomousLimits) *Runtime {
	runtime := &Runtime{
		store:            store,
		openAI:           openAI,
		tools:            tools,
		trace:            tracepkg.NewRecorder(store),
		routerMode:       NormalizeRouterMode(routerMode),
		autonomousLimits: normalizeAutonomousLimits(limits),
	}
	runtime.turnEngine = turnpkg.NewEngine(runtimeTurnModel{runtime: runtime})
	return runtime
}

func DefaultAutonomousLimits() AutonomousLimits {
	return AutonomousLimits{
		MaxIterations:  5,
		MaxRuntime:     5 * time.Minute,
		MaxOutputChars: 60000,
		MaxToolCalls:   20,
	}
}

func (r *Runtime) PrepareChatRun(ctx context.Context, agentID string, conversationID string) (PreparedRun, error) {
	agent, err := r.resolveAgent(strings.TrimSpace(agentID))
	if err != nil {
		return PreparedRun{}, err
	}

	run, err := r.store.CreateRun(agent.ID, conversationID)
	if err != nil {
		return PreparedRun{}, err
	}
	run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		return PreparedRun{}, err
	}
	r.publishRunLifecycle(ctx, run, domain.EventRunCreated, map[string]any{"status": domain.RunQueued})
	r.publishRunLifecycle(ctx, run, domain.EventRunStarted, map[string]any{"status": run.Status})

	registry, err := r.tools.Registry(ctx)
	if err != nil {
		_, _ = r.FailRun(run.ID, err)
		return PreparedRun{}, err
	}
	agentRegistry, err := registry.EnabledSubset(agent.Tools)
	if err != nil {
		_, _ = r.FailRun(run.ID, err)
		return PreparedRun{}, err
	}

	return PreparedRun{Agent: agent, Run: run, Registry: agentRegistry}, nil
}

func (r *Runtime) StreamChat(ctx context.Context, prepared PreparedRun, history []domain.Message, latest string, executorKind string) (<-chan openai.StreamEvent, <-chan error) {
	if strings.TrimSpace(executorKind) == "" {
		executorKind = prepared.Agent.Executor
	}
	executor := r.executorFor(executorKind)
	retrievedMemories, retrievedChunks := r.retrieveContext(ctx, prepared.Run.ID, latest, prepared.Agent.MemoryEnabled, prepared.Agent.RetrievalEnabled, map[string]any{
		"agent_id":           prepared.Agent.ID,
		"agent_name":         prepared.Agent.Name,
		"executor":           executor.Kind(),
		"framework":          executor.Framework(),
		"memory_enabled":     prepared.Agent.MemoryEnabled,
		"retrieval_enabled":  prepared.Agent.RetrievalEnabled,
		"configured_tools":   prepared.Agent.Tools,
		"enabled_tool_count": enabledToolCount(prepared.Registry),
	})
	events := make(chan openai.StreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		_, err := r.turnEngine.Execute(ctx, turnpkg.Request{
			RunID:          prepared.Run.ID,
			ConversationID: prepared.Run.ConversationID,
			Agent:          prepared.Agent,
			SystemPrompt:   prepared.Agent.SystemPrompt,
			History:        history,
			Input:          latest,
			ExecutorKind:   executor.Kind(),
			Registry:       prepared.Registry,
			Context: turnpkg.Context{
				Memories: retrievedMemories,
				Chunks:   retrievedChunks,
			},
			Sink: r.runEventSink(),
		}, func(event turnpkg.Event) {
			if event.Type == turnpkg.EventModelDelta {
				events <- openai.StreamEvent{Type: "delta", Delta: event.Delta}
			}
		})
		if err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func enabledToolCount(registry *tools.Registry) int {
	if registry == nil {
		return 0
	}
	return len(registry.EnabledNames())
}

func (r *Runtime) runEventSink() eventpkg.Sink { return eventpkg.StoreSink{Store: r.store} }

func (r *Runtime) publishRunLifecycle(ctx context.Context, run domain.Run, eventType domain.RunEventType, payload map[string]any) {
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: eventType, RunID: run.ID, ConversationID: run.ConversationID, Payload: payload})
}

func (r *Runtime) publishStage(ctx context.Context, step domain.CollaborationStep, eventType domain.RunEventType) {
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: eventType, RunID: step.RunID, ConversationID: step.ConversationID, StageID: step.ID, Payload: map[string]any{
		"name": step.Role, "agent_id": step.AgentID, "iteration": step.Iteration,
		"input": step.Input, "output": step.Output, "error": step.Error,
	}})
}

func (r *Runtime) retrieveContext(ctx context.Context, runID string, query string, memoryEnabled bool, retrievalEnabled bool, metadata map[string]any) ([]domain.RetrievedMemory, []domain.RetrievedDocumentChunk) {
	conversationID := ""
	if run, ok, _ := r.store.GetRun(runID); ok {
		conversationID = run.ConversationID
	}
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventRetrievalStarted, RunID: runID, ConversationID: conversationID, Payload: map[string]any{"query": truncateRuntimeText(query, 1200)}})
	embeddingQuery := truncateRetrievalEmbeddingQuery(query)
	payload := map[string]any{
		"query":                          truncateRuntimeText(query, 1200),
		"embedding_query_chars":          len(embeddingQuery),
		"embedding_query_original_chars": len(query),
		"embedding_query_truncated":      len(embeddingQuery) < len(query),
	}
	for key, value := range metadata {
		payload[key] = value
	}
	if !memoryEnabled && !retrievalEnabled {
		payload["memory_count"] = 0
		payload["chunk_count"] = 0
		r.trace.Retrieval(ctx, runID, "", payload)
		_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventRetrievalCompleted, RunID: runID, ConversationID: conversationID, Payload: payload})
		return nil, nil
	}
	embedding, err := r.openAI.EmbedText(ctx, embeddingQuery)
	if err != nil {
		r.trace.Error(ctx, runID, "", map[string]any{
			"source": "memory_retrieval",
			"stage":  "embed_query",
			"error":  err.Error(),
		})
		_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventRetrievalFailed, RunID: runID, ConversationID: conversationID, Payload: map[string]any{"error": err.Error()}})
		return nil, nil
	}
	payload["embedding_provider"] = embedding.Provider
	payload["embedding_model"] = embedding.Model
	payload["embedding_dimensions"] = embedding.Dimensions
	payload["embedding_estimated"] = embedding.Estimated
	var memories []domain.RetrievedMemory
	if memoryEnabled {
		var err error
		memories, err = r.store.SearchMemories(domain.MemorySearch{
			Query:             query,
			Embedding:         embedding.Vector,
			EmbeddingProvider: embedding.Provider,
			EmbeddingModel:    embedding.Model,
			Limit:             5,
		})
		if err != nil {
			r.trace.Error(ctx, runID, "", map[string]any{
				"source": "memory_retrieval",
				"stage":  "search",
				"error":  err.Error(),
			})
			memories = nil
		}
	}
	var chunks []domain.RetrievedDocumentChunk
	if retrievalEnabled {
		var err error
		chunks, err = r.store.SearchDocumentChunks(domain.DocumentSearch{
			Query:             query,
			Embedding:         embedding.Vector,
			EmbeddingProvider: embedding.Provider,
			EmbeddingModel:    embedding.Model,
			Limit:             5,
		})
		if err != nil {
			r.trace.Error(ctx, runID, "", map[string]any{
				"source": "rag_retrieval",
				"stage":  "search",
				"error":  err.Error(),
			})
			chunks = nil
		}
	}
	payload["memory_count"] = len(memories)
	payload["chunk_count"] = len(chunks)
	for key, value := range retrievalTracePayload(memories, chunks) {
		payload[key] = value
	}
	r.trace.Retrieval(ctx, runID, "", payload)
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventRetrievalCompleted, RunID: runID, ConversationID: conversationID, Payload: payload})
	return memories, chunks
}

func (r *Runtime) executorFor(kind string) AgentExecutor {
	switch NormalizeExecutorKind(kind) {
	case ExecutorLangChainGo:
		return LangChainGoExecutor{openAI: r.openAI, trace: r.trace}
	default:
		return NativeExecutor{openAI: r.openAI, trace: r.trace}
	}
}

func promptWithRetrievedContext(systemPrompt string, memories []domain.RetrievedMemory, chunks []domain.RetrievedDocumentChunk) string {
	systemPrompt = strings.TrimSpace(systemPrompt)
	if len(memories) > 0 {
		systemPrompt += "\n\n" + formatRuntimeRetrievedMemories(memories)
	}
	if len(chunks) > 0 {
		systemPrompt += "\n\n" + formatRuntimeRetrievedChunks(chunks)
	}
	return strings.TrimSpace(systemPrompt)
}

func retrievalTracePayload(memories []domain.RetrievedMemory, chunks []domain.RetrievedDocumentChunk) map[string]any {
	payload := map[string]any{}
	if len(memories) > 0 {
		items := make([]map[string]any, 0, len(memories))
		for _, memory := range memories {
			items = append(items, map[string]any{
				"id":         memory.Memory.ID,
				"kind":       memory.Memory.Kind,
				"content":    truncateRuntimeText(memory.Memory.Content, 1200),
				"metadata":   memory.Memory.Metadata,
				"similarity": memory.Similarity,
				"score":      memory.Score,
			})
		}
		payload["retrieved_memories"] = items
	}
	if len(chunks) > 0 {
		items := make([]map[string]any, 0, len(chunks))
		for _, chunk := range chunks {
			items = append(items, map[string]any{
				"document_id":    chunk.Document.ID,
				"document_title": chunk.Document.Title,
				"chunk_id":       chunk.Chunk.ID,
				"chunk_index":    chunk.Chunk.ChunkIndex,
				"content":        truncateRuntimeText(chunk.Chunk.Content, 1600),
				"metadata":       chunk.Chunk.Metadata,
				"similarity":     chunk.Similarity,
				"score":          chunk.Score,
			})
		}
		payload["retrieved_chunks"] = items
	}
	return payload
}

func formatRuntimeRetrievedMemories(memories []domain.RetrievedMemory) string {
	var builder strings.Builder
	builder.WriteString("Retrieved memories. Use them when relevant, and ignore them when they are not relevant:\n")
	for index, memory := range memories {
		builder.WriteString(fmt.Sprintf("%d. id=%s kind=%s score=%.4f\n", index+1, memory.Memory.ID, memory.Memory.Kind, memory.Score))
		builder.WriteString(truncateRuntimeText(memory.Memory.Content, 1200))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func formatRuntimeRetrievedChunks(chunks []domain.RetrievedDocumentChunk) string {
	var builder strings.Builder
	builder.WriteString("Retrieved document chunks. Use them when relevant, and ignore them when they are not relevant:\n")
	for index, chunk := range chunks {
		builder.WriteString(fmt.Sprintf("%d. document=%s chunk=%s score=%.4f\n", index+1, chunk.Document.Title, chunk.Chunk.ID, chunk.Score))
		builder.WriteString(truncateRuntimeText(chunk.Chunk.Content, 1600))
		builder.WriteString("\n")
	}
	return strings.TrimSpace(builder.String())
}

func truncateRuntimeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func truncateRetrievalEmbeddingQuery(value string) string {
	const maxEmbeddingQueryChars = 3000
	value = strings.TrimSpace(value)
	if len(value) <= maxEmbeddingQueryChars {
		return value
	}
	return value[:maxEmbeddingQueryChars]
}

func (r *Runtime) CompleteRun(id string) (domain.Run, error) {
	run, err := r.store.UpdateRunStatus(id, domain.RunCompleted, "")
	if err == nil {
		r.publishRunLifecycle(context.Background(), run, domain.EventRunCompleted, map[string]any{"status": run.Status})
	}
	return run, err
}

func (r *Runtime) FailRun(id string, err error) (domain.Run, error) {
	message := ""
	if err != nil {
		message = err.Error()
		r.trace.Error(context.Background(), id, "", map[string]any{
			"source": "runtime",
			"error":  message,
		})
	}
	run, updateErr := r.store.UpdateRunStatus(id, domain.RunFailed, message)
	if updateErr == nil {
		r.publishRunLifecycle(context.Background(), run, domain.EventRunFailed, map[string]any{"status": run.Status, "error": message})
	}
	return run, updateErr
}

func (r *Runtime) CancelRun(id string) (domain.Run, error) {
	run, ok, err := r.store.GetRun(strings.TrimSpace(id))
	if err != nil {
		return domain.Run{}, err
	}
	if !ok {
		return domain.Run{}, store.ErrNotFound("run")
	}
	switch run.Status {
	case domain.RunCompleted, domain.RunFailed, domain.RunCanceled:
		return run, nil
	case domain.RunRunning:
		updated, err := r.store.UpdateRunStatus(run.ID, domain.RunCanceling, "cancel requested by user")
		if err == nil {
			r.publishRunLifecycle(context.Background(), updated, domain.EventRunCancelRequested, map[string]any{"status": updated.Status})
		}
		return updated, err
	default:
		updated, err := r.store.UpdateRunStatus(run.ID, domain.RunCanceled, "canceled by user")
		if err == nil {
			r.publishRunLifecycle(context.Background(), updated, domain.EventRunCanceled, map[string]any{"status": updated.Status})
		}
		return updated, err
	}
}

func (r *Runtime) resolveAgent(agentID string) (domain.Agent, error) {
	if agentID == "" {
		agent, ok, err := r.store.GetDefaultAgent()
		if err != nil {
			return domain.Agent{}, err
		}
		if ok {
			return agent, nil
		}
		return domain.Agent{}, store.ErrNotFound("agent")
	}

	agent, ok, err := r.store.GetAgent(agentID)
	if err != nil {
		return domain.Agent{}, err
	}
	if !ok {
		return domain.Agent{}, store.ErrNotFound("agent")
	}
	if agent.Archived {
		return domain.Agent{}, store.ErrNotFound("agent")
	}
	return agent, nil
}

func normalizeAutonomousLimits(limits AutonomousLimits) AutonomousLimits {
	defaults := DefaultAutonomousLimits()
	if limits.MaxIterations <= 0 {
		limits.MaxIterations = defaults.MaxIterations
	}
	if limits.MaxRuntime <= 0 {
		limits.MaxRuntime = defaults.MaxRuntime
	}
	if limits.MaxOutputChars <= 0 {
		limits.MaxOutputChars = defaults.MaxOutputChars
	}
	if limits.MaxToolCalls <= 0 {
		limits.MaxToolCalls = defaults.MaxToolCalls
	}
	return limits
}
