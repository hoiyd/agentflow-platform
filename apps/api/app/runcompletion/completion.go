package runcompletion

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

// Lifecycle is the existing Runtime's terminal-state capability, not a second engine.
type Lifecycle interface {
	CompleteRun(string) (domain.Run, error)
	FailRun(string, error) (domain.Run, error)
	RejectRunCompletion(string, domain.RunStatus, string) (domain.Run, error)
}

type Verifier interface {
	FreezeContract(*domain.CompletionContract) (*domain.CompletionContract, error)
	Verify(context.Context, string, verification.Subject) (verification.Decision, error)
}

type Dependencies struct {
	Runtime      Lifecycle
	Verification Verifier
}

type Request struct {
	WorkspaceID    string
	RunID          string
	ConversationID string
	UserInput      string
	Assistant      string
	UserMessage    *domain.Message
	GenerateTitle  bool
}

func FreezeContract(verifier Verifier, contract *domain.CompletionContract) (*domain.CompletionContract, error) {
	if contract == nil {
		return nil, nil
	}
	if verifier == nil {
		return nil, errors.New("verification engine is unavailable")
	}
	return verifier.FreezeContract(contract)
}

// Complete persists the candidate before gating completion. Citation events and
// title generation retain their best-effort semantics; transport is not an input.
func Complete(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, request Request) (domain.ChatChunk, error) {
	sources, citations, invalidCitationIDs, err := ResolveCitations(scoped, request.RunID, request.Assistant)
	if err != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}
	webSources, webCitations, invalidWebCitationIDs, err := resolveWebCitations(scoped, request.RunID, request.Assistant)
	if err != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}
	message, err := scoped.AddMessageWithSources(request.ConversationID, "assistant", request.Assistant, citations, webCitations)
	if err != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}
	_, _ = scoped.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCitationResolved, RunID: request.RunID, ConversationID: request.ConversationID,
		Payload: map[string]any{
			"protocol_version":     domain.RAGCitationProtocolVersion,
			"available_source_ids": citationSourceIDs(sources),
			"cited_source_ids":     citationSourceIDs(citations),
			"invalid_source_ids":   invalidCitationIDs,
			"message_id":           message.ID,
		},
	})
	if len(webSources)+len(invalidWebCitationIDs) > 0 {
		_, _ = scoped.CreateRunEvent(domain.RunEvent{
			Type: domain.EventCitationResolved, RunID: request.RunID, ConversationID: request.ConversationID,
			Payload: map[string]any{
				"protocol_version":     domain.WebCitationProtocolVersion,
				"available_source_ids": webCitationSourceIDs(webSources),
				"cited_source_ids":     webCitationSourceIDs(webCitations),
				"invalid_source_ids":   invalidWebCitationIDs,
				"web_citations":        webCitations,
				"message_id":           message.ID,
			},
		})
	}

	completed, err := Resolve(ctx, scoped, dependencies, request.RunID, request.UserInput, request.Assistant)
	if err != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}

	title := ""
	if request.GenerateTitle {
		title = SummarizeTitle(ctx, scoped, dependencies.Runtime, request.RunID, request.ConversationID, request.UserInput, request.Assistant)
	}
	return domain.ChatChunk{
		Type:                  "done",
		ConversationID:        completed.ConversationID,
		Title:                 title,
		RunID:                 completed.ID,
		AgentID:               completed.AgentID,
		Status:                string(completed.Status),
		VerificationStatus:    string(completed.VerificationStatus),
		MessageID:             message.ID,
		Citations:             citations,
		InvalidCitationIDs:    invalidCitationIDs,
		WebCitations:          webCitations,
		InvalidWebCitationIDs: invalidWebCitationIDs,
	}, nil
}

func Resolve(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, runID, question, output string) (domain.Run, error) {
	run, ok, err := scoped.GetRun(runID)
	if err != nil {
		return domain.Run{}, err
	}
	if !ok {
		return domain.Run{}, errors.New("run not found")
	}
	// Verification is opt-in per Run. Server verifier configuration only makes
	// implementations available; it never changes an uncontracted chat Run.
	if run.CompletionContract == nil {
		return dependencies.Runtime.CompleteRun(runID)
	}
	if dependencies.Verification == nil {
		return domain.Run{}, errors.New("verification engine is unavailable")
	}
	if strings.TrimSpace(question) == "" {
		messages, listErr := scoped.ListMessages(run.ConversationID)
		if listErr != nil {
			return domain.Run{}, listErr
		}
		question = latestUserInput(messages)
	}
	result, err := verifyCandidate(ctx, scoped, dependencies, run, question, output)
	return result.Run, err
}

func latestAssistantOutput(messages []domain.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "assistant" && strings.TrimSpace(messages[index].Content) != "" {
			return messages[index].Content
		}
	}
	return ""
}

func latestUserInput(messages []domain.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" && strings.TrimSpace(messages[index].Content) != "" {
			return messages[index].Content
		}
	}
	return ""
}

type VerificationResult struct {
	Run      domain.Run
	Decision verification.Decision
}

var (
	ErrNotRequired = errors.New("run does not require verification")
	ErrTerminal    = errors.New("terminal run cannot be reverified")
	ErrNoCandidate = errors.New("run has no candidate output to verify")
)

// Reverify uses fresh evidence for the latest durable candidate without
// resaving the answer or failing the Run on verifier infrastructure errors.
func Reverify(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, run domain.Run) (VerificationResult, error) {
	if run.CompletionContract == nil {
		return VerificationResult{}, ErrNotRequired
	}
	if run.Status == domain.RunCompleted || run.Status == domain.RunCanceled {
		return VerificationResult{}, ErrTerminal
	}
	messages, err := scoped.ListMessages(run.ConversationID)
	if err != nil {
		return VerificationResult{}, err
	}
	output := latestAssistantOutput(messages)
	if output == "" {
		return VerificationResult{}, ErrNoCandidate
	}
	return verifyCandidate(ctx, scoped, dependencies, run, latestUserInput(messages), output)
}

func verifyCandidate(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, run domain.Run, question, output string) (VerificationResult, error) {
	if dependencies.Verification == nil {
		return VerificationResult{}, errors.New("verification engine is unavailable")
	}
	decision, err := dependencies.Verification.Verify(ctx, run.ID, verificationSubjectForRun(scoped, run, question, output))
	if err != nil {
		_, _ = scoped.UpdateRunVerificationStatus(run.ID, domain.VerificationBlocked)
		return VerificationResult{}, err
	}
	if decision.AllowCompletion {
		run, err = dependencies.Runtime.CompleteRun(run.ID)
	} else {
		run, err = dependencies.Runtime.RejectRunCompletion(run.ID, decision.RunStatus, decision.Summary)
	}
	return VerificationResult{Run: run, Decision: decision}, err
}
