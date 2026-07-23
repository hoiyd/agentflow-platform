package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	tracepkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
	turnpkg "agentflow-platform/apps/api/internal/turn"
)

type Runtime struct {
	store                 RuntimeStore
	openAI                *openai.Client
	tools                 *tools.Manager
	trace                 *eventpkg.Recorder
	turnEngine            *turnpkg.Engine
	routerMode            string
	contextAssemblyConfig domain.ContextAssemblyConfig
	contextCompactor      *contextcompaction.Compactor
	autonomousLimits      AutonomousLimits
	runBudget             domain.RuntimeRunBudget
	knowledgeRetriever    rag.Retriever
}

type RuntimeStore interface {
	ListAgents() ([]domain.Agent, error)
	ListMessages(string) ([]domain.Message, error)
	GetAgent(string) (domain.Agent, bool, error)
	GetDefaultAgent() (domain.Agent, bool, error)
	CreateRun(string, string, domain.RuntimeSnapshot) (domain.Run, error)
	CreateRunWithContract(string, string, domain.RuntimeSnapshot, *domain.CompletionContract) (domain.Run, error)
	UpdateRunAgent(string, string) (domain.Run, error)
	UpdateRunStatus(string, domain.RunStatus, string) (domain.Run, error)
	UpdateRunHeartbeat(string) (domain.Run, error)
	GetRun(string) (domain.Run, bool, error)
	CreateCollaborationStep(domain.CollaborationStep) (domain.CollaborationStep, error)
	UpdateCollaborationStep(string, domain.CollaborationStepStatus, string, string) (domain.CollaborationStep, error)
	UpdateCollaborationStepOutput(string, string) (domain.CollaborationStep, error)
	ListCollaborationSteps(string) ([]domain.CollaborationStep, error)
	eventpkg.RunEventStore
	ListRunEvents(string) ([]domain.RunEvent, error)
	store.ContextCompactionStore
	store.RunUsageStore
	SearchMemories(domain.MemorySearch) ([]domain.RetrievedMemory, error)
}

type AutonomousLimits struct {
	MaxIterations  int
	MaxRuntime     time.Duration
	MaxOutputChars int
	MaxToolCalls   int
}

type PreparedRun struct {
	Agent   domain.Agent
	Run     domain.Run
	Catalog *tools.Catalog
}

// RuntimeOptions captures the complete runtime policy at construction time so
// new Runs can freeze one coherent snapshot without post-construction setters.
type RuntimeOptions struct {
	Store              RuntimeStore
	ModelClient        *openai.Client
	Tools              *tools.Manager
	RouterMode         string
	ContextAssembly    domain.ContextAssemblyConfig
	Autonomous         AutonomousLimits
	RunBudget          domain.RuntimeRunBudget
	KnowledgeRetriever rag.Retriever
}

func NewRuntime(options RuntimeOptions) *Runtime {
	knowledgeRetriever := options.KnowledgeRetriever
	if knowledgeRetriever == nil {
		if searchStore, ok := options.Store.(rag.SearchStore); ok {
			knowledgeRetriever = rag.NewRetrievalPipeline(searchStore)
		}
	}
	runtime := &Runtime{
		store:                 options.Store,
		openAI:                options.ModelClient,
		tools:                 options.Tools,
		trace:                 tracepkg.NewRecorder(options.Store),
		routerMode:            NormalizeRouterMode(options.RouterMode),
		contextAssemblyConfig: contextassembly.NormalizeConfig(options.ContextAssembly),
		contextCompactor:      contextcompaction.NewCompactor(options.Store),
		autonomousLimits:      normalizeAutonomousLimits(options.Autonomous),
		runBudget:             options.RunBudget,
		knowledgeRetriever:    knowledgeRetriever,
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

func (r *Runtime) PrepareChatRun(ctx context.Context, agentID string, conversationID string, executorKind string) (PreparedRun, error) {
	return r.PrepareChatRunWithContract(ctx, agentID, conversationID, executorKind, nil)
}

func (r *Runtime) PrepareChatRunWithContract(ctx context.Context, agentID string, conversationID string, executorKind string, contract *domain.CompletionContract) (PreparedRun, error) {
	agent, err := r.resolveAgent(strings.TrimSpace(agentID))
	if err != nil {
		return PreparedRun{}, err
	}

	snapshot, err := r.captureRuntimeSnapshot(ChatModeSingle, agent, nil, executorKind)
	if err != nil {
		return PreparedRun{}, err
	}
	agent = restoreAgent(snapshot.Agent)
	run, err := r.store.CreateRunWithContract(agent.ID, conversationID, snapshot, contract)
	if err != nil {
		return PreparedRun{}, err
	}
	run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		return PreparedRun{}, err
	}
	r.publishRunLifecycle(ctx, run, domain.EventRunCreated, map[string]any{"status": domain.RunQueued})
	r.publishRunLifecycle(ctx, run, domain.EventRunStarted, map[string]any{"status": run.Status})

	restored, err := r.restoreRuntime(run)
	if err != nil {
		_, _ = r.FailRun(run.ID, err)
		return PreparedRun{}, err
	}

	return PreparedRun{Agent: agent, Run: run, Catalog: restored.catalog}, nil
}

func (r *Runtime) StreamChat(ctx context.Context, prepared PreparedRun, history []domain.Message, latest string, executorKind string) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	errs := make(chan error, 1)
	client, err := r.clientForRun(prepared.Run.ID)
	if err != nil {
		close(events)
		errs <- err
		close(errs)
		return events, errs
	}
	executorKind = prepared.Agent.Executor
	executor := r.executorFor(executorKind, client)
	catalog := prepared.Catalog
	if catalog == nil {
		catalog, _ = tools.NewCatalog()
	}
	retrievedMemories, retrievedChunks := r.retrieveContext(ctx, prepared.Run.ID, latest, prepared.Agent.MemoryEnabled, prepared.Agent.RetrievalEnabled, map[string]any{
		"agent_id":           prepared.Agent.ID,
		"agent_name":         prepared.Agent.Name,
		"executor":           executor.Kind(),
		"framework":          executor.Framework(),
		"memory_enabled":     prepared.Agent.MemoryEnabled,
		"retrieval_enabled":  prepared.Agent.RetrievalEnabled,
		"configured_tools":   prepared.Agent.Tools,
		"enabled_tool_count": enabledToolCount(prepared.Catalog),
	})
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
			Catalog:        catalog,
			Context: turnpkg.Context{
				Memories: retrievedMemories,
				Chunks:   retrievedChunks,
			},
			Sink: r.runEventSink(),
		}, func(event turnpkg.Event) {
			if event.Type == turnpkg.EventModelDelta {
				events <- domain.RunEvent{
					Type:           domain.EventModelDelta,
					SchemaVersion:  domain.CurrentRunEventSchemaVersion,
					RunID:          prepared.Run.ID,
					ConversationID: prepared.Run.ConversationID,
					Payload:        map[string]any{"delta": event.Delta},
					Timestamp:      event.Timestamp,
				}
			}
		})
		if err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func enabledToolCount(catalog *tools.Catalog) int {
	if catalog == nil {
		return 0
	}
	return len(catalog.EnabledNames())
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
	embeddingQuery := rag.EmbeddingQuery(query)
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
		_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventRetrievalCompleted, RunID: runID, ConversationID: conversationID, Payload: payload})
		return nil, nil
	}
	client, err := r.clientForRun(runID)
	if err != nil {
		_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventRetrievalFailed, RunID: runID, ConversationID: conversationID, Payload: map[string]any{"error": err.Error()}})
		return nil, nil
	}
	embedding, err := rag.EmbedQuery(ctx, query, func(ctx context.Context, query string) (rag.Embedding, error) {
		result, embedErr := client.EmbedText(ctx, query)
		return rag.Embedding{
			Vector:     result.Vector,
			Provider:   result.Provider,
			Model:      result.Model,
			Dimensions: result.Dimensions,
			Estimated:  result.Estimated,
		}, embedErr
	})
	if err != nil {
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
			payload["memory_error"] = err.Error()
			memories = nil
		}
	}
	var chunks []domain.RetrievedDocumentChunk
	if retrievalEnabled {
		if r.knowledgeRetriever == nil {
			payload["rag_error"] = "knowledge retriever is not configured"
		} else {
			response, searchErr := r.knowledgeRetriever.Search(domain.DocumentSearch{
				Query: query,
				Limit: 5,
			}, 5, embedding)
			if searchErr != nil {
				payload["rag_error"] = searchErr.Error()
			} else {
				chunks = response.Items
				payload["fusion"] = response.Fusion
				payload["knowledge_security"] = response.Security
				payload["rag_no_match"] = response.NoMatch
				if response.Reason != "" {
					payload["rag_no_match_reason"] = response.Reason
				}
			}
		}
	}
	payload["memory_count"] = len(memories)
	payload["chunk_count"] = len(chunks)
	for key, value := range retrievalTracePayload(memories, chunks) {
		payload[key] = value
	}
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: domain.EventRetrievalCompleted, RunID: runID, ConversationID: conversationID, Payload: payload})
	return memories, chunks
}

func (r *Runtime) executorFor(kind string, client *openai.Client) AgentExecutor {
	switch NormalizeExecutorKind(kind) {
	case ExecutorLangChainGo:
		return LangChainGoExecutor{openAI: client, trace: r.trace}
	default:
		return NativeExecutor{openAI: client, trace: r.trace}
	}
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
				"vector_rank":    chunk.VectorRank,
				"lexical_rank":   chunk.LexicalRank,
				"lexical_score":  chunk.LexicalScore,
				"rrf_score":      chunk.RRFScore,
				"fusion_rank":    chunk.FusionRank,
				"rerank_rank":    chunk.RerankRank,
				"rerank_score":   chunk.RerankScore,
				"confidence":     chunk.Confidence,
				"filter_reason":  chunk.FilterReason,
			})
		}
		payload["retrieved_chunks"] = items
	}
	return payload
}

func truncateRuntimeText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func (r *Runtime) CompleteRun(id string) (domain.Run, error) {
	run, err := r.store.UpdateRunStatus(id, domain.RunCompleted, "")
	if err == nil {
		r.publishRunLifecycle(context.Background(), run, domain.EventRunCompleted, map[string]any{"status": run.Status})
		r.scheduleSoftContextCompaction(run)
	}
	return run, err
}

func (r *Runtime) FailRun(id string, err error) (domain.Run, error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	run, updateErr := r.store.UpdateRunStatus(id, domain.RunFailed, message)
	if updateErr == nil {
		r.publishRunLifecycle(context.Background(), run, domain.EventRunFailed, map[string]any{"status": run.Status, "error": message})
	}
	return run, updateErr
}

func (r *Runtime) RejectRunCompletion(id string, status domain.RunStatus, reason string) (domain.Run, error) {
	eventType := domain.EventRunFailed
	switch status {
	case domain.RunFailed, domain.RunFailedRecoverable:
	case domain.RunWaitingForUser:
		eventType = domain.EventRunWaitingForUser
	default:
		return domain.Run{}, fmt.Errorf("invalid verification rejection status %q", status)
	}
	run, err := r.store.UpdateRunStatus(id, status, strings.TrimSpace(reason))
	if err == nil {
		r.publishRunLifecycle(context.Background(), run, eventType, map[string]any{
			"status": run.Status, "error": run.Error, "source": "completion_gate",
		})
	}
	return run, err
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
