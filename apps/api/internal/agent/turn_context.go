package agent

import (
	"context"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/sessionhistory"
	"agentflow-platform/apps/api/internal/skill"
)

type turnContextStore interface {
	sessionhistory.Store
	GetRun(string) (domain.Run, bool, error)
	GetLatestContextCompaction(string) (domain.ContextCompaction, bool, error)
	ListRunEvents(string) ([]domain.RunEvent, error)
	GetDocumentInWorkspace(string, string) (domain.Document, []domain.DocumentChunk, bool, error)
}

type guidanceValidator interface {
	Validate(context.Context, string, *domain.ProjectGuidanceSnapshot) error
}

// Session construction knows only source capabilities, not model dispatch,
// routing, Tool execution or compaction scheduling. Loaders stay round-local.
type turnContextSessions struct {
	store    turnContextStore
	sink     eventpkg.Sink
	guidance guidanceValidator
	steering interface {
		ConsumeSteering(context.Context, string, string, string) ([]domain.RunInput, error)
	}
	skills    *skill.Service
	taskState interface {
		Get(string) (domain.TaskState, bool, error)
	}
	knowledgeReads bool
}

func (s turnContextSessions) withSession(ctx context.Context, request turn.Request, snapshot *domain.RuntimeSnapshot) (context.Context, *domain.ContextCompaction) {
	isolated := request.Context.Isolated
	history := request.History
	if isolated {
		history = nil
	}
	var compaction *domain.ContextCompaction
	if !isolated && snapshot.ContextAssembly.CompactionMode != contextassembly.CompactionModeOff {
		if latest, ok, loadErr := s.store.GetLatestContextCompaction(request.ConversationID); loadErr == nil && ok {
			compaction = &latest
		}
	}
	var historySearch []domain.RetrievedSessionHistory
	if !isolated {
		historySearch = s.retrieveSessionHistory(ctx, request.RunID, request.ConversationID, request.Input)
	}
	session := contextassembly.Session{
		ProjectGuidance: snapshot.ProjectGuidance,
		AgentID:         request.Agent.ID,
		Config:          snapshot.ContextAssembly, Sink: request.Sink,
		History: history, CurrentInput: request.Input,
		SkillInput: request.SkillInput,
		Memories:   request.Context.Memories, Knowledge: request.Context.Chunks,
		HistorySearch: historySearch,
		Compaction:    compaction,
	}
	if snapshot.ProjectGuidance != nil {
		session.ValidateProjectGuidance = func() error {
			run, ok, err := s.store.GetRun(request.RunID)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("project instructions Run is unavailable")
			}
			return validateProjectGuidance(ctx, s.guidance, run)
		}
	}
	if !isolated && s.steering != nil {
		session.LoadSteering = func() ([]domain.RunInput, error) {
			return s.steering.ConsumeSteering(ctx, request.RunID, request.StepID, request.TurnID)
		}
	}
	if request.Role != "planner" && (len(request.Agent.Skills) > 0 || strings.HasPrefix(strings.TrimSpace(request.SkillInput), "/skill:")) {
		if bound, err := skill.Bound(snapshot, request.Agent.ID); err == nil {
			for _, item := range bound {
				session.Skills = append(session.Skills, skill.Metadata(item))
			}
		}
		session.LoadSkills = func() ([]domain.SkillSnapshot, error) { return s.skills.Active(ctx, snapshot) }
	}
	if !isolated && s.taskState != nil {
		session.LoadTaskState = func() (domain.TaskState, bool, error) {
			return s.taskState.Get(request.ConversationID)
		}
	}
	if s.knowledgeReads {
		session.LoadKnowledgeReads = func() ([]domain.KnowledgeToolReadResult, error) {
			return s.loadKnowledgeReads(request)
		}
	}
	return contextassembly.WithSession(ctx, session), compaction
}

func (s turnContextSessions) loadKnowledgeReads(request turn.Request) ([]domain.KnowledgeToolReadResult, error) {
	run, ok, err := s.store.GetRun(request.RunID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, eligibility.ErrSourceUnavailable
	}
	events, err := s.store.ListRunEvents(request.RunID)
	if err != nil {
		return nil, err
	}
	// Recheck even this Turn's paired observations before every continuation.
	if err := eligibility.ValidateSources(run.RuntimeSnapshot, run.WorkspaceID, rag.RunCitationReservations(events), func(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
		return s.store.GetDocumentInWorkspace(run.WorkspaceID, id)
	}); err != nil {
		return nil, err
	}
	reads := []domain.KnowledgeToolReadResult{}
	for _, item := range events {
		if item.TurnID == request.TurnID {
			continue // This Turn already receives its own paired Tool observations.
		}
		if read, ok := rag.KnowledgeReadFromEvent(item); ok {
			reads = append(reads, read)
		}
	}
	return reads, nil
}
