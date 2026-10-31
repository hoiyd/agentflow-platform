package runcompletion

import (
	"context"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/planning"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

// Lifecycle is the existing Runtime's terminal-state capability, not a second engine.
type Lifecycle interface {
	CompleteRun(string) (domain.Run, error)
	CompleteVerifiedRun(string) (domain.Run, error)
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

// Complete persists the candidate before gating completion. Contracted Runs also
// require a durable subject binding; transport is not an input.
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
	run, ok, err := scoped.GetRun(request.RunID)
	if err == nil && !ok {
		err = store.ErrNotFound("run")
	}
	if err != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}
	question, err := completionQuestion(scoped, run, request.UserInput)
	if err != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}
	payload := map[string]any{
		"protocol_version":     domain.RAGCitationProtocolVersion,
		"available_source_ids": citationSourceIDs(sources),
		"cited_source_ids":     citationSourceIDs(citations),
		"invalid_source_ids":   invalidCitationIDs,
		"message_id":           message.ID,
	}
	if run.CompletionContract != nil {
		payload["verification_question"] = question
		payload["verification_subject_hash"] = candidateBindingHash(run.ID, message.ID, question, request.Assistant)
	}
	_, err = scoped.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCitationResolved, RunID: request.RunID, ConversationID: request.ConversationID,
		Payload: payload,
	})
	if err != nil && run.CompletionContract != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}
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

	completed, err := resolve(ctx, scoped, dependencies, run, question, request.Assistant)
	if err != nil {
		_, _ = dependencies.Runtime.FailRun(request.RunID, err)
		return domain.ChatChunk{}, err
	}

	title := ""
	if request.GenerateTitle {
		title = SummarizeTitle(ctx, scoped, dependencies.Runtime, request.RunID, request.ConversationID, request.UserInput, request.Assistant)
	}
	resultURL := ""
	if completed.CompletionContract != nil && completed.CompletionContract.Output != nil {
		resultURL = "/api/runs/" + completed.ID + "/result"
	}
	return domain.ChatChunk{
		ResultURL:             resultURL,
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
	question, err = completionQuestion(scoped, run, question)
	if err != nil {
		return domain.Run{}, err
	}
	return resolve(ctx, scoped, dependencies, run, question, output)
}

func resolve(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, run domain.Run, question, output string) (domain.Run, error) {
	if run.RuntimeSnapshot != nil && run.RuntimeSnapshot.ExecutionConstraint == domain.ExecutionPlanOnly {
		items, err := scoped.ListRunEvents(run.ID)
		if err != nil {
			return domain.Run{}, err
		}
		if planning.Read(items) == nil {
			plan, err := planning.Record(run, question, output, items)
			if err != nil {
				return domain.Run{}, err
			}
			item, err := event.NewRunEvent(domain.EventPlanRecorded, event.EventMetadata{RunID: run.ID, ConversationID: run.ConversationID}, event.PlanPayload{RunPlan: plan})
			if err != nil {
				return domain.Run{}, err
			}
			if _, err := scoped.CreateRunEvent(item); err != nil {
				return domain.Run{}, err
			}
		}
	}
	// Verification is opt-in per Run. Server verifier configuration only makes
	// implementations available; it never changes an uncontracted chat Run.
	if run.CompletionContract == nil {
		return dependencies.Runtime.CompleteRun(run.ID)
	}
	if dependencies.Verification == nil {
		return domain.Run{}, errors.New("verification engine is unavailable")
	}
	result, err := verifyCandidate(ctx, scoped, dependencies, run, question, output)
	return result.Run, err
}

func completionQuestion(scoped store.WorkspaceStore, run domain.Run, question string) (string, error) {
	planOnly := run.RuntimeSnapshot != nil && run.RuntimeSnapshot.ExecutionConstraint == domain.ExecutionPlanOnly
	if strings.TrimSpace(question) != "" || run.CompletionContract == nil && !planOnly {
		return question, nil
	}
	messages, err := scoped.ListMessages(run.ConversationID)
	if err != nil {
		return "", err
	}
	for _, message := range messages {
		if message.Role == "user" && !message.CreatedAt.After(run.CreatedAt) {
			question = message.Content
		}
	}
	items, err := scoped.ListRunEvents(run.ID)
	if err != nil {
		return "", err
	}
	return clarification.AddAnswers(question, items)
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

// Reverify uses fresh evidence for this Run's bound durable candidate without
// resaving the answer or failing the Run on verifier infrastructure errors.
func Reverify(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, run domain.Run) (VerificationResult, error) {
	if run.CompletionContract == nil {
		return VerificationResult{}, ErrNotRequired
	}
	if run.Status == domain.RunCompleted || run.Status == domain.RunCanceled {
		return VerificationResult{}, ErrTerminal
	}
	bound, err := boundCandidate(scoped, run)
	if err != nil {
		return VerificationResult{}, err
	}
	if run.RuntimeSnapshot != nil && run.RuntimeSnapshot.ExecutionConstraint == domain.ExecutionPlanOnly {
		plan := planning.Read(bound.events)
		if plan == nil || plan.RunID != run.ID || plan.Version != planning.Version || plan.Input != bound.question || plan.Proposal != bound.output {
			return VerificationResult{}, ErrNoCandidate
		}
	}
	subject := verificationSubjectForRun(scoped, run, bound.question, bound.output)
	if err := validateSubjectEvidence(scoped, run, subject, bound); err != nil {
		return VerificationResult{}, err
	}
	return verifySubject(ctx, scoped, dependencies, run, subject)
}

func verifyCandidate(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, run domain.Run, question, output string) (VerificationResult, error) {
	return verifySubject(ctx, scoped, dependencies, run, verificationSubjectForRun(scoped, run, question, output))
}

func verifySubject(ctx context.Context, scoped store.WorkspaceStore, dependencies Dependencies, run domain.Run, subject verification.Subject) (VerificationResult, error) {
	if dependencies.Verification == nil {
		return VerificationResult{}, errors.New("verification engine is unavailable")
	}
	decision, err := dependencies.Verification.Verify(ctx, run.ID, subject)
	if err != nil {
		_, updateErr := scoped.UpdateRunVerificationStatus(run.ID, domain.VerificationBlocked)
		return VerificationResult{}, errors.Join(err, updateErr)
	}
	if decision.AllowCompletion {
		run, err = dependencies.Runtime.CompleteVerifiedRun(run.ID)
	} else {
		run, err = dependencies.Runtime.RejectRunCompletion(run.ID, decision.RunStatus, decision.Summary)
	}
	return VerificationResult{Run: run, Decision: decision}, err
}
