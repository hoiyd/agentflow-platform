package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	turnpkg "agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/contextcompaction"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	tracepkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/inference/routing"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/artifact"
	"agentflow-platform/apps/api/internal/tool/progress"
)

type Runtime struct {
	store                 RuntimeStore
	embeddingClient       provider.Client
	modelRoutes           *routing.Catalog
	modelRoutesErr        error
	tools                 *tool.Manager
	trace                 *eventpkg.Recorder
	turnEngine            *turnpkg.Engine
	routerMode            string
	contextAssemblyConfig domain.ContextAssemblyConfig
	contextCompactor      *contextcompaction.Compactor
	autonomousLimits      AutonomousLimits
	runBudget             domain.RuntimeRunBudget
	toolProgressConfig    progress.Config
	toolExecutionOptions  tool.ExecutorOptions
	toolProgressMu        sync.Mutex
	toolProgressGuards    map[string]*progress.Guard
	knowledgeRetriever    rag.Retriever
	checkpoints           checkpoint.Provider
	taskStates            *taskstate.Service
	toolArtifacts         *artifact.Service
	liveEvents            eventpkg.LivePublisher
	memoryRecall          memorypkg.Recaller
	activeRunCancels      sync.Map
}

type activeRunCancellation struct {
	cancel context.CancelFunc
}

type RuntimeStore interface {
	ListAgents() ([]domain.Agent, error)
	ListMessages(string) ([]domain.Message, error)
	GetAgent(string) (domain.Agent, bool, error)
	GetDefaultAgent() (domain.Agent, bool, error)
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
	store.SessionHistoryStore
	store.RunUsageStore
	store.CheckpointStore
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
	Catalog *tool.Catalog
}

// RuntimeOptions captures the complete runtime policy at construction time so
// new Runs can freeze one coherent snapshot without post-construction setters.
type RuntimeOptions struct {
	Store RuntimeStore
	// ModelClient is a one-route compatibility shorthand for existing tests.
	// Production composition supplies ModelRoutes and EmbeddingClient separately.
	ModelClient        provider.Client
	EmbeddingClient    provider.Client
	ModelRoutes        *routing.Catalog
	Tools              *tool.Manager
	RouterMode         string
	ContextAssembly    domain.ContextAssemblyConfig
	Autonomous         AutonomousLimits
	RunBudget          domain.RuntimeRunBudget
	ToolProgressGuard  progress.Config
	ToolExecution      tool.ExecutorOptions
	KnowledgeRetriever rag.Retriever
	CheckpointProvider checkpoint.Provider
	LiveEvents         eventpkg.LivePublisher
	MemoryRecall       memorypkg.Recaller
}

func NewRuntime(options RuntimeOptions) *Runtime {
	modelRoutes := options.ModelRoutes
	var modelRoutesErr error
	if modelRoutes == nil {
		modelRoutes, modelRoutesErr = singleModelRouteCatalog(options.ModelClient, options.ContextAssembly, options.RunBudget)
	}
	embeddingClient := options.EmbeddingClient
	if embeddingClient == nil {
		embeddingClient = options.ModelClient
	}
	progressConfig := options.ToolProgressGuard
	if strings.TrimSpace(progressConfig.Version) == "" {
		progressConfig = progress.DefaultConfig()
	} else {
		progressConfig = progress.NormalizeConfig(progressConfig)
	}
	knowledgeRetriever := options.KnowledgeRetriever
	if knowledgeRetriever == nil {
		if searchStore, ok := options.Store.(rag.SearchStore); ok {
			knowledgeRetriever = rag.NewRetrievalPipeline(searchStore)
		}
	}
	checkpointProvider := options.CheckpointProvider
	if checkpointProvider == nil {
		checkpointProvider = checkpoint.NewInternalProvider(options.Store)
	}
	var taskStates *taskstate.Service
	if taskStore, ok := options.Store.(taskstate.Store); ok {
		taskStates = taskstate.NewService(taskStore, eventpkg.StoreSink{Store: options.Store})
	}
	var toolArtifacts *artifact.Service
	if artifactStore, ok := options.Store.(store.ToolArtifactStore); ok {
		toolArtifacts = artifact.NewService(artifactStore, tracepkg.NewRecorder(options.Store))
	}
	runtime := &Runtime{
		store:                 options.Store,
		embeddingClient:       embeddingClient,
		modelRoutes:           modelRoutes,
		modelRoutesErr:        modelRoutesErr,
		tools:                 options.Tools,
		trace:                 tracepkg.NewRecorder(options.Store),
		routerMode:            NormalizeRouterMode(options.RouterMode),
		contextAssemblyConfig: contextassembly.NormalizeConfig(options.ContextAssembly),
		contextCompactor:      contextcompaction.NewCompactor(options.Store),
		autonomousLimits:      normalizeAutonomousLimits(options.Autonomous),
		runBudget:             options.RunBudget,
		toolProgressConfig:    progressConfig,
		toolExecutionOptions:  options.ToolExecution,
		toolProgressGuards:    map[string]*progress.Guard{},
		knowledgeRetriever:    knowledgeRetriever,
		checkpoints:           checkpointProvider,
		taskStates:            taskStates,
		toolArtifacts:         toolArtifacts,
		liveEvents:            options.LiveEvents,
		memoryRecall:          options.MemoryRecall,
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

func (r *Runtime) PrepareChatRunWithContract(ctx context.Context, agentID string, conversationID string, contract *domain.CompletionContract) (PreparedRun, error) {
	agent, err := r.resolveAgent(strings.TrimSpace(agentID))
	if err != nil {
		return PreparedRun{}, err
	}

	snapshot, err := r.captureRuntimeSnapshot(ChatModeSingle, agent, nil)
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

func (r *Runtime) StreamChat(ctx context.Context, prepared PreparedRun, history []domain.Message, latest string) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	errs := make(chan error, 1)
	executionCtx, releaseCancellation := r.bindRunCancellation(ctx, prepared.Run.ID)
	catalog := prepared.Catalog
	if catalog == nil {
		catalog, _ = tool.NewCatalog()
	}
	retrievedMemories, retrievedChunks := r.retrieveContext(executionCtx, prepared.Run.ID, userInputRetrievalQuery(latest), prepared.Agent.MemoryEnabled, prepared.Agent.RetrievalEnabled, map[string]any{
		"agent_id":           prepared.Agent.ID,
		"agent_name":         prepared.Agent.Name,
		"executor":           domain.DefaultAgentExecutor,
		"framework":          "agentflow-native",
		"memory_enabled":     prepared.Agent.MemoryEnabled,
		"retrieval_enabled":  prepared.Agent.RetrievalEnabled,
		"configured_tools":   prepared.Agent.Tools,
		"enabled_tool_count": enabledToolCount(prepared.Catalog),
	})
	go func() {
		defer close(events)
		defer close(errs)
		defer releaseCancellation()
		_, err := r.turnEngine.Execute(executionCtx, turnpkg.Request{
			RunID:          prepared.Run.ID,
			ConversationID: prepared.Run.ConversationID,
			Agent:          prepared.Agent,
			SystemPrompt:   prepared.Agent.SystemPrompt,
			History:        history,
			Input:          latest,
			Catalog:        catalog,
			Context: turnpkg.Context{
				Memories: retrievedMemories,
				Chunks:   retrievedChunks,
			},
			Sink: r.runEventSink(),
		}, func(event turnpkg.Event) {
			if event.Type == turnpkg.EventModelDelta {
				live := domain.RunEvent{
					Type:           domain.EventModelDelta,
					SchemaVersion:  domain.CurrentRunEventSchemaVersion,
					RunID:          prepared.Run.ID,
					ConversationID: prepared.Run.ConversationID,
					Payload:        map[string]any{"delta": event.Delta},
					Timestamp:      event.Timestamp,
				}
				r.publishLive(live)
				events <- live
			}
		})
		if err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func enabledToolCount(catalog *tool.Catalog) int {
	if catalog == nil {
		return 0
	}
	return len(catalog.EnabledNames())
}

func (r *Runtime) runEventSink() eventpkg.Sink {
	return eventpkg.StoreSink{Store: r.store, Live: r.liveEvents}
}

func (r *Runtime) publishLive(item domain.RunEvent) {
	if r != nil && r.liveEvents != nil {
		r.liveEvents.PublishLive(item)
	}
}

func (r *Runtime) publishRunLifecycle(ctx context.Context, run domain.Run, eventType domain.RunEventType, payload map[string]any) {
	_ = r.runEventSink().Publish(ctx, domain.RunEvent{Type: eventType, RunID: run.ID, ConversationID: run.ConversationID, Payload: payload})
}

func (r *Runtime) publishStage(ctx context.Context, step domain.CollaborationStep, eventType domain.RunEventType) error {
	if r.checkpoints == nil {
		return fmt.Errorf("checkpoint provider is unavailable")
	}
	_, err := r.checkpoints.RecordStageTransition(ctx, step, eventType)
	return err
}

func (r *Runtime) CompleteRun(id string) (domain.Run, error) {
	run, err := r.store.UpdateRunStatus(id, domain.RunCompleted, "")
	if err == nil {
		r.publishRunLifecycle(context.Background(), run, domain.EventRunCompleted, map[string]any{"status": run.Status})
		r.scheduleSoftContextCompaction(run)
		r.forgetProgressGuard(run.ID)
	}
	return run, err
}

func (r *Runtime) FailRun(id string, err error) (domain.Run, error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	if current, ok, loadErr := r.store.GetRun(id); loadErr == nil && ok && current.Status == domain.RunCanceling && errors.Is(err, context.Canceled) {
		run, updateErr := r.store.UpdateRunStatus(id, domain.RunCanceled, message)
		if updateErr == nil {
			r.publishRunLifecycle(context.Background(), run, domain.EventRunCanceled, failure.Merge(map[string]any{"status": run.Status, "error": message}, err))
			r.forgetProgressGuard(run.ID)
		}
		return run, updateErr
	}
	run, updateErr := r.store.UpdateRunStatus(id, domain.RunFailed, message)
	if updateErr == nil {
		r.publishRunLifecycle(context.Background(), run, domain.EventRunFailed, failure.Merge(map[string]any{"status": run.Status, "error": message}, err))
		r.forgetProgressGuard(run.ID)
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

func (r *Runtime) bindRunCancellation(ctx context.Context, runID string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	active := &activeRunCancellation{cancel: cancel}
	if previous, loaded := r.activeRunCancels.Swap(runID, active); loaded {
		previous.(*activeRunCancellation).cancel()
	}
	return ctx, func() {
		r.activeRunCancels.CompareAndDelete(runID, active)
		cancel()
	}
}

func (r *Runtime) cancelActiveRun(runID string) {
	if active, ok := r.activeRunCancels.Load(runID); ok {
		active.(*activeRunCancellation).cancel()
	}
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
		r.forgetProgressGuard(run.ID)
		return run, nil
	case domain.RunRunning:
		updated, err := r.store.UpdateRunStatus(run.ID, domain.RunCanceling, "cancel requested by user")
		if err == nil {
			r.publishRunLifecycle(context.Background(), updated, domain.EventRunCancelRequested, map[string]any{"status": updated.Status})
			r.cancelActiveRun(run.ID)
		}
		return updated, err
	case domain.RunCanceling:
		r.cancelActiveRun(run.ID)
		return run, nil
	default:
		updated, err := r.store.UpdateRunStatus(run.ID, domain.RunCanceled, "canceled by user")
		if err == nil {
			r.publishRunLifecycle(context.Background(), updated, domain.EventRunCanceled, map[string]any{"status": updated.Status})
			r.forgetProgressGuard(updated.ID)
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
