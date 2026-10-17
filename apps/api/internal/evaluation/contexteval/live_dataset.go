package contexteval

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

const liveDatasetSchema = "compaction-continuation-dataset-v1"

type liveDataset struct {
	SchemaVersion string     `json:"schema_version"`
	ID            string     `json:"id"`
	Version       string     `json:"version"`
	Synthetic     bool       `json:"synthetic"`
	Cases         []liveCase `json:"cases"`
}

type liveCase struct {
	ID              string                        `json:"id"`
	History         []liveMessage                 `json:"history"`
	CurrentInput    string                        `json:"current_input"`
	StateSource     string                        `json:"state_source"`
	StatePatch      domain.TaskStatePatch         `json:"state_patch"`
	Material        liveMaterial                  `json:"material"`
	ExpectedFacts   map[string]liveClaim          `json:"expected_facts"`
	FactDefinitions map[string]liveFactDefinition `json:"fact_definitions,omitempty"`
}

type liveFactDefinition struct {
	Description string `json:"description"`
	TaskID      string `json:"task_id,omitempty"`
}

type liveMessage struct {
	ID            string `json:"id"`
	Role          string `json:"role"`
	Content       string `json:"content"`
	PaddingRepeat int    `json:"padding_repeat,omitempty"`
}

type liveMaterial struct {
	ID            string `json:"id"`
	Content       string `json:"content"`
	RequiredQuote string `json:"required_quote"`
}

type liveClaim struct {
	Value    json.RawMessage `json:"value"`
	SourceID string          `json:"source_id"`
	Quote    string          `json:"quote"`
}

func loadLiveDataset(path string) (liveDataset, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return liveDataset{}, "", err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1024*1024+1))
	if err != nil {
		return liveDataset{}, "", err
	}
	if len(b) > 1024*1024 {
		return liveDataset{}, "", errors.New("live context dataset exceeds 1 MiB")
	}
	var data liveDataset
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return data, "", err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return data, "", errors.New("dataset contains trailing JSON")
	}
	if data.SchemaVersion != liveDatasetSchema || data.ID == "" || data.Version == "" || !data.Synthetic || len(data.Cases) == 0 || len(data.Cases) > 20 {
		return data, "", errors.New("require versioned synthetic compaction dataset with 1-20 cases")
	}
	if err := redaction.ValidateValue(data); err != nil {
		return data, "", err
	}
	seen := map[string]bool{}
	for _, c := range data.Cases {
		if seen[c.ID] || c.ID == "" || strings.TrimSpace(c.CurrentInput) == "" || len(c.History) < 8 || len(c.History) > 100 || len(c.ExpectedFacts) == 0 || len(c.ExpectedFacts) > 32 || c.Material.ID == "" || c.Material.RequiredQuote == "" || !strings.Contains(c.Material.Content, c.Material.RequiredQuote) {
			return data, "", fmt.Errorf("invalid live case %q", c.ID)
		}
		seen[c.ID] = true
		sources := map[string]string{}
		userSources := map[string]bool{}
		for i, m := range c.History {
			// v1 admits only complete user/assistant exchanges; arbitrary persisted
			// Tool fields cannot be silently flattened into apparently fair history.
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			if m.ID == "" || sources[m.ID] != "" || m.Role != role || strings.TrimSpace(m.Content) == "" || m.PaddingRepeat < 0 || m.PaddingRepeat > 500 {
				return data, "", fmt.Errorf("case %s has an invalid protocol boundary", c.ID)
			}
			sources[m.ID] = m.Content
			userSources[m.ID] = m.Role == "user"
		}
		if len(c.History)%2 != 0 || !userSources[c.StateSource] || c.StatePatch.ExpectedVersion != 0 {
			return data, "", errors.New("state requires an explicit user source and a complete continuation boundary")
		}
		state, err := domain.ApplyTaskStatePatch(domain.EmptyTaskState("1", "validation"), c.StatePatch, timeForState)
		if err != nil {
			return data, "", err
		}
		for name, fact := range c.ExpectedFacts {
			if name == "" || !json.Valid(fact.Value) || fact.Quote == "" || !strings.Contains(sources[fact.SourceID], fact.Quote) {
				return data, "", fmt.Errorf("fact %s lacks exact source evidence", name)
			}
		}
		for name, definition := range c.FactDefinitions {
			fact, exists := c.ExpectedFacts[name]
			if !exists || strings.TrimSpace(definition.Description) == "" {
				return data, "", fmt.Errorf("fact definition %s requires a known field and description", name)
			}
			if definition.TaskID == "" {
				continue
			}
			var status domain.TaskItemStatus
			if err := json.Unmarshal(fact.Value, &status); err != nil {
				return data, "", fmt.Errorf("task fact %s must be a lifecycle status", name)
			}
			found := false
			for _, task := range state.Tasks {
				if task.ID == definition.TaskID && task.Status == status {
					found = true
					break
				}
			}
			if !found {
				return data, "", fmt.Errorf("task fact %s does not match its declared Task State task", name)
			}
		}
	}
	return data, liveDigest(b), nil
}

func liveDigest(v any) string {
	b, ok := v.([]byte)
	if !ok {
		b, _ = json.Marshal(v)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}
