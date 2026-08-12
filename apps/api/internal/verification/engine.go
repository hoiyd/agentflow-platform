package verification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

const maxArtifactsPerEvidence = 8

type Store interface {
	GetRun(string) (domain.Run, bool, error)
	UpdateRunVerificationStatus(string, domain.VerificationStatus) (domain.Run, error)
	AppendVerificationRecord(domain.VerificationRecord) error
	ListVerificationEvidence(string) ([]domain.VerificationEvidence, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}

type Engine struct {
	store    Store
	registry *Registry
}

type Decision struct {
	Status          domain.VerificationStatus `json:"status"`
	AllowCompletion bool                      `json:"allow_completion"`
	Attempt         int                       `json:"attempt"`
	SubjectHash     string                    `json:"subject_hash"`
	RunStatus       domain.RunStatus          `json:"run_status"`
	Summary         string                    `json:"summary"`
}

func NewEngine(store Store, registry *Registry) *Engine {
	return &Engine{store: store, registry: registry}
}

func (e *Engine) FreezeContract(input *domain.CompletionContract) (*domain.CompletionContract, error) {
	if e == nil || e.registry == nil {
		if input == nil {
			return nil, nil
		}
		return nil, &VerificationError{Kind: ErrorUnavailable, Message: "verification engine is unavailable"}
	}
	return e.registry.FreezeContract(input)
}

func (e *Engine) Verify(ctx context.Context, runID string, subject Subject) (Decision, error) {
	if e == nil || e.store == nil || e.registry == nil {
		return Decision{}, &VerificationError{Kind: ErrorUnavailable, Message: "verification engine is unavailable"}
	}
	run, ok, err := e.store.GetRun(strings.TrimSpace(runID))
	if err != nil {
		return Decision{}, err
	}
	if !ok {
		return Decision{}, &VerificationError{Kind: ErrorUnavailable, Message: "run not found"}
	}
	if run.CompletionContract == nil {
		return Decision{Status: domain.VerificationNotRequired, AllowCompletion: true, RunStatus: domain.RunCompleted, Summary: "verification is not required"}, nil
	}
	if subject.Type != run.CompletionContract.SubjectType || subject.Hash == "" {
		return Decision{}, invalidContract("verification subject does not match the frozen contract")
	}
	if err := e.validateFrozenContract(run.CompletionContract); err != nil {
		return Decision{}, err
	}

	previous, err := e.store.ListVerificationEvidence(run.ID)
	if err != nil {
		return Decision{}, err
	}
	attempt := nextAttempt(previous)
	if attempt > run.CompletionContract.Policy.MaxAttempts {
		return exhaustedDecision(run.CompletionContract.Policy, attempt-1, subject.Hash), nil
	}
	if _, err := e.store.UpdateRunVerificationStatus(run.ID, domain.VerificationRunning); err != nil {
		return Decision{}, err
	}
	e.recordEvent(run, domain.EventVerificationRequested, map[string]any{
		"contract_id": run.CompletionContract.ID, "contract_version": run.CompletionContract.Version,
		"contract_hash": run.CompletionContract.Hash, "subject_hash": subject.Hash, "attempt": attempt,
	})
	if err := e.markStaleEvidence(run, previous, subject, attempt); err != nil {
		return Decision{}, err
	}

	snapshotHash, err := SnapshotHash(run.RuntimeSnapshot)
	if err != nil {
		return Decision{}, err
	}
	results := make(map[string]domain.VerificationStatus, len(run.CompletionContract.Verifiers))
	for _, spec := range run.CompletionContract.Verifiers {
		status, err := e.runVerifier(ctx, run, spec, subject, snapshotHash, attempt)
		if err != nil {
			return Decision{}, err
		}
		results[spec.ID] = status
	}
	decision := evaluateGate(*run.CompletionContract, results, attempt, subject.Hash)
	if _, err := e.store.UpdateRunVerificationStatus(run.ID, decision.Status); err != nil {
		return Decision{}, err
	}
	if !decision.AllowCompletion && attempt < run.CompletionContract.Policy.MaxAttempts {
		e.recordEvent(run, domain.EventRunRevisionRequested, map[string]any{
			"attempt": attempt, "max_attempts": run.CompletionContract.Policy.MaxAttempts,
			"subject_hash": subject.Hash, "verification_status": decision.Status, "summary": decision.Summary,
		})
	}
	return decision, nil
}

func (e *Engine) validateFrozenContract(contract *domain.CompletionContract) error {
	if contract.Version != domain.CurrentCompletionContractVersion {
		return invalidContract(fmt.Sprintf("unsupported completion contract version %d", contract.Version))
	}
	hash, err := completionContractHash(*contract)
	if err != nil {
		return err
	}
	if hash != contract.Hash {
		return invalidContract("stored completion contract hash does not match its effective definition")
	}
	return nil
}

func (e *Engine) markStaleEvidence(run domain.Run, previous []domain.VerificationEvidence, subject Subject, attempt int) error {
	alreadyMarked := make(map[string]bool)
	for _, item := range previous {
		if item.Status == domain.VerificationStale && item.SupersedesEvidenceID != "" {
			alreadyMarked[item.SupersedesEvidenceID] = true
		}
	}
	for _, item := range previous {
		if item.Status == domain.VerificationStale || item.SubjectHash == subject.Hash || alreadyMarked[item.ID] {
			continue
		}
		now := time.Now().UTC()
		marker := domain.VerificationEvidence{
			ID: newID("evidence"), RunID: run.ID, StageID: item.StageID,
			ContractID: run.CompletionContract.ID, ContractVersion: run.CompletionContract.Version,
			VerifierID: item.VerifierID, VerifierType: item.VerifierType, VerifierVersion: item.VerifierVersion,
			Attempt: attempt, SubjectHash: subject.Hash, SnapshotHash: item.SnapshotHash,
			Status: domain.VerificationStale, StartedAt: now, CompletedAt: now,
			Summary: "subject changed; previous evidence is stale", Details: map[string]any{}, ArtifactIDs: []string{}, SupersedesEvidenceID: item.ID,
		}
		if err := e.store.AppendVerificationRecord(domain.VerificationRecord{Evidence: marker, Artifacts: []domain.VerificationArtifact{}}); err != nil {
			return err
		}
		e.recordEvent(run, domain.EventVerificationStale, evidencePayload(marker))
	}
	return nil
}

func (e *Engine) runVerifier(ctx context.Context, run domain.Run, spec domain.VerifierSpec, subject Subject, snapshotHash string, attempt int) (domain.VerificationStatus, error) {
	verifier, ok := e.registry.Resolve(spec.Type)
	e.recordEvent(run, domain.EventVerificationStarted, map[string]any{
		"verifier_id": spec.ID, "verifier_type": spec.Type, "verifier_version": spec.Version,
		"subject_hash": subject.Hash, "attempt": attempt,
	})
	started := time.Now().UTC()
	result := blocked(BlockedImplementationMissing, "frozen verifier implementation is unavailable")
	if ok && verifier.Version() == spec.Version {
		verifyCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.TimeoutMS)*time.Millisecond)
		result = verifier.Verify(verifyCtx, spec, subject)
		cancel()
	}
	completed := time.Now().UTC()
	if result.Status != domain.VerificationPassed && result.Status != domain.VerificationFailed && result.Status != domain.VerificationBlocked {
		result = blocked(BlockedInvalidResult, "verifier returned an invalid status")
	}

	evidenceID := newID("evidence")
	artifacts := buildArtifacts(run.ID, evidenceID, result.Artifacts, completed, e.registry.maxArtifactBytes)
	details := cloneDetails(result.Details, e.registry.maxArtifactBytes)
	if omitted := len(result.Artifacts) - len(artifacts); omitted > 0 {
		details["artifacts_omitted"] = omitted
	}
	artifactIDs := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		artifactIDs = append(artifactIDs, artifact.ID)
	}
	evidence := domain.VerificationEvidence{
		ID: evidenceID, RunID: run.ID, ContractID: run.CompletionContract.ID,
		ContractVersion: run.CompletionContract.Version, VerifierID: spec.ID,
		VerifierType: spec.Type, VerifierVersion: spec.Version, Attempt: attempt,
		SubjectHash: subject.Hash, SnapshotHash: snapshotHash, Status: result.Status,
		StartedAt: started, CompletedAt: completed, DurationMS: completed.Sub(started).Milliseconds(),
		ExitCode: result.ExitCode, Summary: strings.TrimSpace(result.Summary), Details: details, ArtifactIDs: artifactIDs,
	}
	if err := e.store.AppendVerificationRecord(domain.VerificationRecord{Evidence: evidence, Artifacts: artifacts}); err != nil {
		return domain.VerificationBlocked, err
	}
	eventType := domain.EventVerificationPassed
	if result.Status == domain.VerificationFailed {
		eventType = domain.EventVerificationFailed
	}
	if result.Status == domain.VerificationBlocked {
		eventType = domain.EventVerificationBlocked
	}
	e.recordEvent(run, eventType, evidencePayload(evidence))
	return result.Status, nil
}

func buildArtifacts(runID, evidenceID string, outputs []Artifact, createdAt time.Time, limit int) []domain.VerificationArtifact {
	if len(outputs) > maxArtifactsPerEvidence {
		outputs = outputs[:maxArtifactsPerEvidence]
	}
	artifacts := make([]domain.VerificationArtifact, 0, len(outputs))
	for _, output := range outputs {
		originalContent := output.Content
		content, contentChanged := boundedArtifactContent(output.Content, limit)
		truncated := output.Truncated || contentChanged
		byteSize := output.ByteSize
		if byteSize == 0 {
			byteSize = len(originalContent)
		}
		contentHash := output.ContentHash
		if contentHash == "" {
			contentHash = hashBytes([]byte(originalContent))
		}
		kind := strings.TrimSpace(output.Kind)
		if kind == "" {
			kind = "verifier_output"
		}
		mediaType := strings.TrimSpace(output.MediaType)
		if mediaType == "" {
			mediaType = "text/plain; charset=utf-8"
		}
		artifacts = append(artifacts, domain.VerificationArtifact{
			ID: newID("artifact"), RunID: runID, EvidenceID: evidenceID,
			Kind: kind, MediaType: mediaType, Content: content,
			ContentHash: contentHash, ByteSize: byteSize,
			Truncated: truncated, CreatedAt: createdAt,
		})
	}
	return artifacts
}

func boundedArtifactContent(content string, limit int) (string, bool) {
	original := content
	if !utf8.ValidString(content) {
		content = strings.ToValidUTF8(content, "\uFFFD")
	}
	if limit > 0 && len(content) > limit {
		content = content[:limit]
		for !utf8.ValidString(content) {
			content = content[:len(content)-1]
		}
	}
	return content, content != original
}

func evaluateGate(contract domain.CompletionContract, results map[string]domain.VerificationStatus, attempt int, subjectHash string) Decision {
	required := 0
	passed := 0
	blockedCount := 0
	failedCount := 0
	for _, spec := range contract.Verifiers {
		if !spec.Required {
			continue
		}
		required++
		switch results[spec.ID] {
		case domain.VerificationPassed:
			passed++
		case domain.VerificationBlocked:
			blockedCount++
		default:
			failedCount++
		}
	}
	allow := passed == required
	if contract.Policy.Mode == domain.VerificationAnyMayPass {
		allow = passed > 0
	}
	if allow {
		return Decision{Status: domain.VerificationPassed, AllowCompletion: true, Attempt: attempt, SubjectHash: subjectHash, RunStatus: domain.RunCompleted,
			Summary: fmt.Sprintf("completion gate passed: %d/%d required verifiers passed", passed, required)}
	}
	status := domain.VerificationFailed
	if blockedCount > 0 && failedCount == 0 {
		status = domain.VerificationBlocked
	}
	decision := Decision{Status: status, Attempt: attempt, SubjectHash: subjectHash, RunStatus: domain.RunFailedRecoverable,
		Summary: fmt.Sprintf("completion gate rejected candidate: %d passed, %d failed, %d blocked", passed, failedCount, blockedCount)}
	if attempt >= contract.Policy.MaxAttempts {
		decision = exhaustedDecision(contract.Policy, attempt, subjectHash)
		decision.Status = status
		decision.Summary = fmt.Sprintf("completion gate exhausted after %d attempt(s): %d passed, %d failed, %d blocked", attempt, passed, failedCount, blockedCount)
	}
	return decision
}

func exhaustedDecision(policy domain.VerificationPolicy, attempt int, subjectHash string) Decision {
	runStatus := domain.RunFailed
	if policy.OnExhausted == domain.VerificationWaitForUser {
		runStatus = domain.RunWaitingForUser
	}
	return Decision{Status: domain.VerificationFailed, Attempt: attempt, SubjectHash: subjectHash, RunStatus: runStatus, Summary: "verification attempt budget is exhausted"}
}

func nextAttempt(evidence []domain.VerificationEvidence) int {
	maxAttempt := 0
	for _, item := range evidence {
		if item.Status != domain.VerificationStale && item.Attempt > maxAttempt {
			maxAttempt = item.Attempt
		}
	}
	return maxAttempt + 1
}

func evidencePayload(evidence domain.VerificationEvidence) map[string]any {
	return map[string]any{
		"evidence_id": evidence.ID, "contract_id": evidence.ContractID,
		"contract_version": evidence.ContractVersion, "verifier_id": evidence.VerifierID,
		"verifier_type": evidence.VerifierType, "verifier_version": evidence.VerifierVersion,
		"attempt": evidence.Attempt, "subject_hash": evidence.SubjectHash,
		"snapshot_hash": evidence.SnapshotHash, "status": evidence.Status,
		"duration_ms": evidence.DurationMS, "summary": evidence.Summary,
		"details": evidence.Details, "artifact_ids": evidence.ArtifactIDs,
		"supersedes_evidence_id": evidence.SupersedesEvidenceID,
	}
}

func cloneDetails(details map[string]any, limit int) map[string]any {
	if details == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return map[string]any{"serialization_error": err.Error()}
	}
	if limit > 0 && len(encoded) > limit {
		return map[string]any{"truncated": true, "byte_size": len(encoded)}
	}
	cloned := make(map[string]any, len(details))
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return map[string]any{"serialization_error": err.Error()}
	}
	return cloned
}

func (e *Engine) recordEvent(run domain.Run, eventType domain.RunEventType, payload map[string]any) {
	_, _ = e.store.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, ConversationID: run.ConversationID, Type: eventType,
		SchemaVersion: domain.CurrentRunEventSchemaVersion, Payload: payload, Timestamp: time.Now().UTC(),
	})
}
