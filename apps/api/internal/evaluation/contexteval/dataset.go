package contexteval

import (
	"agentflow-platform/apps/api/internal/domain"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type dataset struct {
	SchemaVersion string                       `json:"schema_version"`
	ID            string                       `json:"id"`
	Version       string                       `json:"version"`
	Model         string                       `json:"model"`
	Config        domain.ContextAssemblyConfig `json:"config"`
	Cases         []evaluationCase             `json:"cases"`
}

type evaluationCase struct {
	ID                     string             `json:"id"`
	Split                  string             `json:"split"`
	Classification         string             `json:"classification"`
	Scenario               string             `json:"scenario"`
	EvidencePosition       string             `json:"evidence_position"`
	System                 string             `json:"system"`
	SystemRepeat           int                `json:"system_repeat,omitempty"`
	CurrentInput           string             `json:"current_input"`
	History                []fixtureMessage   `json:"history"`
	Compaction             *fixtureCompaction `json:"compaction,omitempty"`
	RequiredFacts          []string           `json:"required_facts"`
	ForbiddenFacts         []string           `json:"forbidden_facts"`
	IrrelevantReferenceIDs []string           `json:"irrelevant_reference_ids"`
	ExpectedErrorCode      string             `json:"expected_error_code,omitempty"`
}

type fixtureMessage struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

type fixtureCompaction struct {
	Summary          string   `json:"summary"`
	SourceMessageIDs []string `json:"source_message_ids"`
}

func loadDataset(path string) (dataset, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return dataset{}, "", fmt.Errorf("read context dataset: %w", err)
	}
	if len(content) > 1<<20 {
		return dataset{}, "", errors.New("context dataset exceeds 1 MiB")
	}
	var data dataset
	if err := json.Unmarshal(content, &data); err != nil {
		return data, "", fmt.Errorf("decode context dataset: %w", err)
	}
	if err := validateDataset(data); err != nil {
		return data, "", err
	}
	sum := sha256.Sum256(content)
	return data, hex.EncodeToString(sum[:]), nil
}

func validateDataset(data dataset) error {
	if data.SchemaVersion != DatasetSchemaVersion || strings.TrimSpace(data.ID) == "" || strings.TrimSpace(data.Version) == "" || strings.TrimSpace(data.Model) == "" || len(data.Cases) == 0 {
		return errors.New("context dataset requires the supported schema, identity, model, and cases")
	}
	seen := map[string]bool{}
	gating := 0
	for _, item := range data.Cases {
		if strings.TrimSpace(item.ID) == "" || seen[item.ID] {
			return fmt.Errorf("context dataset contains an empty or duplicate case ID %q", item.ID)
		}
		seen[item.ID] = true
		if item.Split != "calibration" && item.Split != "holdout" {
			return fmt.Errorf("context case %q requires calibration or holdout split", item.ID)
		}
		if item.Classification != ClassificationGating && item.Classification != ClassificationDiagnostic {
			return fmt.Errorf("context case %q requires gating or diagnostic classification", item.ID)
		}
		if item.Classification == ClassificationGating {
			gating++
		}
		if strings.TrimSpace(item.System) == "" || strings.TrimSpace(item.CurrentInput) == "" {
			return fmt.Errorf("context case %q requires system and current input", item.ID)
		}
		if item.SystemRepeat < 0 || item.SystemRepeat > 100 {
			return fmt.Errorf("context case %q system_repeat must be 0-100", item.ID)
		}
		messageIDs := map[string]bool{}
		for _, message := range item.History {
			if strings.TrimSpace(message.ID) == "" || messageIDs[message.ID] || (message.Role != "user" && message.Role != "assistant") {
				return fmt.Errorf("context case %q contains an invalid history message", item.ID)
			}
			messageIDs[message.ID] = true
		}
		if item.Compaction != nil {
			if strings.TrimSpace(item.Compaction.Summary) == "" || len(item.Compaction.SourceMessageIDs) == 0 {
				return fmt.Errorf("context case %q contains an invalid compaction fixture", item.ID)
			}
			for _, id := range item.Compaction.SourceMessageIDs {
				if !messageIDs[id] {
					return fmt.Errorf("context case %q compaction references unknown message %q", item.ID, id)
				}
			}
		}
	}
	if gating == 0 {
		return errors.New("context dataset contains no gating cases")
	}
	return nil
}
