package failurecorpus

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

// Candidate never copies source content, errors, identities or free-text labels.
// The digest identifies the operator-retained artifact without publishing it.
func Candidate(reader io.Reader, item Case, evidenceRef string) (Case, error) {
	content, err := io.ReadAll(io.LimitReader(reader, (8<<20)+1))
	if err != nil || len(content) > 8<<20 {
		return Case{}, errors.New("source artifact cannot be read or exceeds 8 MiB")
	}
	var source struct {
		Run *struct {
			ID     string
			Status domain.RunStatus
		} `json:"run"`
		ReportFormat  string `json:"report_format"`
		SchemaVersion string `json:"schema_version"`
	}
	if json.Unmarshal(content, &source) != nil {
		return Case{}, errors.New("source artifact must be valid JSON")
	}
	kind := ""
	if source.Run != nil && regexp.MustCompile(`^run_[a-f0-9]+$`).MatchString(source.Run.ID) &&
		slices.Contains([]domain.RunStatus{domain.RunQueued, domain.RunRunning, domain.RunWaitingForUser, domain.RunCompleted, domain.RunFailed, domain.RunFailedRecoverable, domain.RunCanceling, domain.RunCanceled}, source.Run.Status) {
		kind = "replay"
	} else if source.ReportFormat == evalreport.Format && slices.Contains([]string{
		"context-quality-eval-v1", "live-compaction-continuation-v1", "rag-eval-v1", "agent-routing-eval-v1", "task-eval-v1",
	}, source.SchemaVersion) {
		kind = "evaluation"
	}
	if kind == "" {
		return Case{}, errors.New("source must be a supported Replay or evaluation artifact")
	}
	item.SchemaVersion = CaseSchema
	item.Evidence = []Evidence{{Kind: kind, Ref: evidenceRef, SHA256: checksum(content)}}
	item.Review = Review{Status: "pending", Redaction: "pending"}
	if err := validate(item, false); err != nil {
		return Case{}, err
	}
	return item, nil
}
