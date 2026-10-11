package routeeval

import (
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Dataset struct {
	SchemaVersion string           `json:"schema_version"`
	ID            string           `json:"id"`
	Version       string           `json:"version"`
	Agents        []domain.Agent   `json:"agents"`
	Cases         []EvaluationCase `json:"cases"`
}

type EvaluationCase struct {
	ID                 string                          `json:"id"`
	Split              string                          `json:"split"`
	Coverage           []string                        `json:"coverage"`
	Task               string                          `json:"task"`
	Plan               string                          `json:"plan"`
	Requirements       domain.AgentRoutingRequirements `json:"requirements,omitempty"`
	ExpectedOutcome    string                          `json:"expected_outcome"`
	AcceptableAgentIDs []string                        `json:"acceptable_agent_ids,omitempty"`
}

func loadDataset(path string) (Dataset, string, error) {
	if strings.TrimSpace(path) == "" {
		return Dataset{}, "", errors.New("routing evaluation dataset path is required")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return Dataset{}, "", fmt.Errorf("read routing dataset: %w", err)
	}
	var data Dataset
	if err := json.Unmarshal(content, &data); err != nil {
		return Dataset{}, "", fmt.Errorf("parse routing dataset: %w", err)
	}
	if err := validateDataset(&data); err != nil {
		return Dataset{}, "", err
	}
	return data, digest(content), nil
}

func validateDataset(data *Dataset) error {
	if data.SchemaVersion != DatasetSchemaVersion || strings.TrimSpace(data.ID) == "" || strings.TrimSpace(data.Version) == "" || len(data.Agents) < 2 || len(data.Cases) == 0 {
		return fmt.Errorf("routing dataset requires schema %q, identity, at least two agents, and cases", DatasetSchemaVersion)
	}
	agentIDs, caseIDs, splits, coverage := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index := range data.Agents {
		data.Agents[index] = domain.NormalizeAgentConfig(data.Agents[index])
		id := strings.TrimSpace(data.Agents[index].ID)
		if id == "" || agentIDs[id] {
			return fmt.Errorf("routing dataset has missing or duplicate agent id %q", id)
		}
		agentIDs[id] = true
	}
	for _, item := range data.Cases {
		if strings.TrimSpace(item.ID) == "" || caseIDs[item.ID] {
			return fmt.Errorf("routing dataset has missing or duplicate case id %q", item.ID)
		}
		caseIDs[item.ID], splits[item.Split] = true, true
		if strings.TrimSpace(item.Task) == "" || len(item.Coverage) == 0 {
			return fmt.Errorf("case %q requires a task and coverage tags", item.ID)
		}
		for _, tag := range item.Coverage {
			coverage[tag] = true
		}
		if item.Split != SplitCalibration && item.Split != SplitHoldout {
			return fmt.Errorf("case %q has invalid split %q", item.ID, item.Split)
		}
		if item.ExpectedOutcome != agent.AgentSelectionOutcomeSelected && item.ExpectedOutcome != agent.AgentSelectionOutcomeNoEligible && item.ExpectedOutcome != agent.AgentSelectionOutcomeNoSuitable {
			return fmt.Errorf("case %q has invalid expected outcome %q", item.ID, item.ExpectedOutcome)
		}
		if item.ExpectedOutcome == agent.AgentSelectionOutcomeSelected && len(item.AcceptableAgentIDs) == 0 {
			return fmt.Errorf("case %q requires acceptable agents", item.ID)
		}
		for _, id := range item.AcceptableAgentIDs {
			if !agentIDs[id] {
				return fmt.Errorf("case %q references unknown acceptable agent %q", item.ID, id)
			}
		}
	}
	if !splits[SplitCalibration] || !splits[SplitHoldout] {
		return errors.New("routing dataset requires frozen calibration and holdout splits")
	}
	for _, tag := range []string{"clear_single_specialist", "multiple_acceptable_specialists", "insufficient_capability", "no_match", "ambiguous_task", "candidate_description_conflict"} {
		if !coverage[tag] {
			return fmt.Errorf("routing dataset is missing required coverage %q", tag)
		}
	}
	return nil
}

func digest(content []byte) string { sum := sha256.Sum256(content); return hex.EncodeToString(sum[:]) }

func agentCatalogHash(agents []domain.Agent) string {
	content, _ := json.Marshal(agents)
	return digest(content)
}
