package contextcompaction

import (
	"encoding/json"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

const evidenceHeading = "## Source Evidence\n"

var errEvidenceInvalid = failure.New(failure.Definition{
	Message: "context summary evidence is missing, malformed or does not match selected original messages",
	Info:    failure.Info{Code: "context_summary_evidence_invalid", Source: "context_compaction", Category: failure.CategoryValidation},
})

var errEvidenceBudget = failure.New(failure.Definition{
	Message: "context summary budget cannot retain the complete source evidence",
	Info:    failure.Info{Code: "context_summary_evidence_budget_exceeded", Source: "context_compaction", Category: failure.CategoryValidation},
})

var errSummaryOperational = failure.New(failure.Definition{
	Message: "context summary contains unsupported or operational sections; task lifecycle belongs to Task State",
	Info:    failure.Info{Code: "context_summary_operational_content", Source: "context_compaction", Category: failure.CategoryValidation},
})

type sourceEvidence struct {
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	Quote     string `json:"quote"`
}

func parseSourceEvidence(summary string) ([]sourceEvidence, string, error) {
	text, ok := strings.CutPrefix(summary, evidenceHeading)
	if !ok {
		return nil, "", errEvidenceInvalid
	}
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	var evidence []sourceEvidence
	if err := decoder.Decode(&evidence); err != nil || evidence == nil {
		return nil, "", errEvidenceInvalid
	}
	rest := strings.TrimSpace(text[decoder.InputOffset():])
	if (rest != "" && !strings.HasPrefix(rest, "## ")) || strings.Contains(rest, "## Source Evidence") {
		return nil, "", errEvidenceInvalid
	}
	return evidence, rest, nil
}

// This checks attribution and verbatim text, not semantic entailment or whether
// the model selected every important fact. Originals remain the authority.
func validateSourceEvidence(summary string, messages []domain.Message, sourceIDs []string, conversationID string) (string, error) {
	evidence, body, err := parseSourceEvidence(summary)
	if err != nil {
		return "", err
	}
	allowed := make(map[string]bool, len(sourceIDs))
	for _, id := range sourceIDs {
		allowed[id] = true
	}
	sources := make(map[string]domain.Message, len(sourceIDs))
	for _, message := range messages {
		if allowed[message.ID] && message.ConversationID == conversationID {
			sources[message.ID] = message
		}
	}
	for _, item := range evidence {
		source, ok := sources[item.MessageID]
		if !ok || item.Role != source.Role || strings.TrimSpace(item.Quote) == "" || !strings.Contains(source.Content, item.Quote) {
			return "", errEvidenceInvalid
		}
	}
	// A summary is historical context, not a second plan or mutable Task State.
	// Check before clipping, so an oversized tail cannot hide a rejected section.
	for line := range strings.Lines(body) {
		heading := strings.TrimSpace(line)
		if !strings.HasPrefix(heading, "#") {
			continue
		}
		switch heading {
		case "## Established Facts", "## Superseded Instructions", "## Exact References", "## Source References":
		default:
			return "", errSummaryOperational
		}
	}
	// Persist the decoded, validated meaning once, not ambiguous duplicate keys
	// or model-selected JSON formatting. Quote string values are unchanged.
	encoded, _ := json.Marshal(evidence)
	return evidenceHeading + string(encoded) + "\n\n" + body, nil
}
