package contexteval

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/artifact"
)

var timeForState = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type liveFixture struct {
	store    *fixturestore.Store
	run      domain.Run
	history  []domain.Message
	catalog  *tool.Catalog
	recorder *eventpkg.Recorder
	sink     eventpkg.Sink
	config   domain.ContextAssemblyConfig
	input    string
}

func newLiveFixture(ctx context.Context, identity provider.RuntimeIdentity, config domain.ContextAssemblyConfig, limits domain.RuntimeRunBudget, c liveCase, s *LiveSample) (liveFixture, error) {
	f := liveFixture{store: fixturestore.New(), config: config}
	conv, err := f.store.CreateConversation("Compaction continuation evaluation")
	if err != nil {
		return f, err
	}
	s.SourceMessages = map[string]string{}
	for _, m := range c.History {
		content := "source_id=" + m.ID + "\n" + m.Content + strings.Repeat("\nHistorical formatting note: keep headings concise; no new facts or authorization.", m.PaddingRepeat)
		message, err := f.store.AddMessage(conv.ID, m.Role, content)
		if err != nil {
			return f, err
		}
		f.history = append(f.history, message)
		s.SourceMessages[m.ID] = message.ID
	}
	f.recorder = eventpkg.NewRecorder(f.store)
	f.sink = eventpkg.StoreSink{Store: f.store}
	s.TaskState, err = taskstate.NewService(f.store, f.sink).Apply(ctx, conv.ID, c.StatePatch, domain.TaskStateSource{ActorType: "user", SourceMessageID: s.SourceMessages[c.StateSource]})
	if err != nil {
		return f, err
	}
	// Compare content/version, not workspace-specific revision IDs or timestamps.
	state := s.TaskState.State
	state.WorkspaceID = ""
	state.ConversationID = ""
	state.UpdatedAt = time.Time{}
	s.StateHash = liveDigest(state)
	f.catalog, err = tool.NewCatalog(artifact.NewService(f.store, f.recorder).ToolBindings()...)
	if err != nil {
		return f, err
	}
	agentConfig, _, err := f.store.GetAgent("agent_planner")
	if err != nil {
		return f, err
	}
	snapshot := domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, ContextAssembly: config, RunBudget: &limits,
		Mode: "single", ModelRouting: domain.ModelRouteCatalogSnapshot{Routes: []domain.ModelRouteDescriptor{{ID: "evaluation", Model: identity.Model, Provider: identity.Provider, GenerationPolicy: &identity.GenerationPolicy}}}}
	f.run, err = f.store.CreateRunWithContract(agentConfig.ID, conv.ID, snapshot, nil)
	if err != nil {
		return f, err
	}
	s.RunID, s.ConversationID = f.run.ID, conv.ID
	content := []byte(c.Material.Content)
	s.Artifact = domain.ToolArtifact{ID: "tool_artifact_" + f.run.ID, SchemaVersion: domain.CurrentToolArtifactSchemaVersion, RunID: f.run.ID,
		ToolCallID: "frozen-material", ToolName: "evaluation_source", MediaType: "text/plain", ContentHash: liveDigest(content), OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: time.Now().UTC()}
	if _, err = f.store.CreateToolArtifact(s.Artifact, content); err != nil {
		return f, err
	}
	f.input = fmt.Sprintf("%s\nFields: %s\nLogical source %s: read artifact_id %s with artifact_read.\n", c.CurrentInput, factFields(c), c.Material.ID, s.Artifact.ID)
	s.InputHash = liveDigest(c.CurrentInput)
	s.BoundaryHash = liveDigest(struct {
		History  []liveMessage
		Input    string
		Material liveMaterial
		State    string
	}{c.History, c.CurrentInput, c.Material, s.StateHash})
	return f, nil
}

func (f liveFixture) session(ctx context.Context, compaction *domain.ContextCompaction) context.Context {
	return contextassembly.WithSession(ctx, contextassembly.Session{Config: f.config, Sink: f.sink, History: f.history, CurrentInput: f.input, Compaction: compaction,
		LoadTaskState: func() (domain.TaskState, bool, error) { return f.store.GetTaskState(f.run.ConversationID) }})
}

func (f liveFixture) phase(ctx context.Context, name string, purpose domain.RunUsagePurpose, run func(context.Context) (string, error)) LivePhase {
	id := budget.NewOperationID("turn")
	ledger, _, err := f.store.GetRunUsageLedger(f.run.ID)
	if err != nil {
		return LivePhase{Name: name, TurnID: id, ErrorCode: "usage_read_failed"}
	}
	if ledger.Totals.OpenReservations > 0 {
		return LivePhase{Name: name, TurnID: id, ErrorCode: "unsettled_model_usage"}
	}
	ctx = budget.WithScope(ctx, budget.Scope{TurnID: id})
	ctx = budget.WithPurpose(ctx, purpose)
	ctx = eventpkg.WithScope(ctx, eventpkg.Scope{RunID: f.run.ID, ConversationID: f.run.ConversationID, TurnID: id})
	started := time.Now()
	output, err := run(ctx)
	p := LivePhase{Name: name, TurnID: id, DurationMS: time.Since(started).Milliseconds(), Output: output}
	if err != nil {
		p.ErrorCode = liveErrorCode(err)
	}
	return p
}
