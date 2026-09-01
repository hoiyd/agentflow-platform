package tooltest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"agentflow-platform/apps/api/internal/tools"
)

const SelectionDatasetVersion = "tool-selection-v1"

type SelectionDataset struct {
	SchemaVersion string          `json:"schema_version"`
	ID            string          `json:"id"`
	Cases         []SelectionCase `json:"cases"`
}

type SelectionCase struct {
	ID       string               `json:"id"`
	Task     string               `json:"task"`
	Expected SelectionExpectation `json:"expected"`
}

type SelectionExpectation struct {
	Decision         string          `json:"decision"`
	Tool             string          `json:"tool,omitempty"`
	Arguments        json.RawMessage `json:"arguments,omitempty"`
	Outcome          string          `json:"outcome"`
	RequiredEvidence []string        `json:"required_evidence,omitempty"`
	RecoveryAction   string          `json:"recovery_action,omitempty"`
}

type SelectionCandidate struct {
	Decision       string
	Tool           string
	Arguments      json.RawMessage
	Outcome        string
	Evidence       []string
	RecoveryAction string
}

type SelectionFinding struct {
	Code    string
	Message string
}

func ParseSelectionDataset(data []byte) (SelectionDataset, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var dataset SelectionDataset
	if err := decoder.Decode(&dataset); err != nil {
		return SelectionDataset{}, fmt.Errorf("decode Tool selection dataset: %w", err)
	}
	if err := ensureSelectionDatasetEOF(decoder); err != nil {
		return SelectionDataset{}, err
	}
	if dataset.SchemaVersion != SelectionDatasetVersion || strings.TrimSpace(dataset.ID) == "" || len(dataset.Cases) == 0 {
		return SelectionDataset{}, errors.New("Tool selection dataset requires a supported version, ID, and cases")
	}
	seen := make(map[string]bool, len(dataset.Cases))
	for index, item := range dataset.Cases {
		if err := validateSelectionCase(item); err != nil {
			return SelectionDataset{}, fmt.Errorf("case %d: %w", index, err)
		}
		if seen[item.ID] {
			return SelectionDataset{}, fmt.Errorf("duplicate Tool selection case %q", item.ID)
		}
		seen[item.ID] = true
	}
	return dataset, nil
}

func EvaluateSelection(catalog *tools.Catalog, item SelectionCase, candidate SelectionCandidate) []SelectionFinding {
	findings := make([]SelectionFinding, 0)
	if candidate.Decision != item.Expected.Decision {
		findings = append(findings, SelectionFinding{Code: "decision_mismatch", Message: "Tool/no-Tool decision differs from expectation"})
	}
	if item.Expected.Decision == "no_tool" {
		if candidate.Tool != "" {
			findings = append(findings, SelectionFinding{Code: "unexpected_tool", Message: "a Tool was selected for a no-Tool case"})
		}
		if candidate.Outcome != item.Expected.Outcome {
			findings = append(findings, SelectionFinding{Code: "outcome_mismatch", Message: "Tool outcome differs from expectation"})
		}
		return findings
	}
	if candidate.Tool != item.Expected.Tool {
		findings = append(findings, SelectionFinding{Code: "tool_mismatch", Message: "selected Tool differs from expectation"})
	}
	_, contractErr := catalog.ValidateCall(candidate.Tool, candidate.Arguments)
	if item.Expected.Outcome == string(tools.ErrorInvalidArgs) {
		if contractErr == nil || contractErr.Code != tools.ErrorInvalidArgs {
			findings = append(findings, SelectionFinding{Code: "argument_outcome_mismatch", Message: "arguments did not produce the expected validation failure"})
		}
	} else if contractErr != nil {
		findings = append(findings, SelectionFinding{Code: "argument_contract_failed", Message: "selected Tool arguments violate the registered contract"})
	}
	if candidate.Outcome != item.Expected.Outcome {
		findings = append(findings, SelectionFinding{Code: "outcome_mismatch", Message: "Tool outcome differs from expectation"})
	}
	if candidate.RecoveryAction != item.Expected.RecoveryAction {
		findings = append(findings, SelectionFinding{Code: "recovery_mismatch", Message: "failure recovery action differs from expectation"})
	}
	evidence := make(map[string]bool, len(candidate.Evidence))
	for _, value := range candidate.Evidence {
		evidence[value] = true
	}
	for _, required := range item.Expected.RequiredEvidence {
		if !evidence[required] {
			findings = append(findings, SelectionFinding{Code: "required_evidence_missing", Message: "required Tool evidence is missing"})
		}
	}
	return findings
}

func validateSelectionCase(item SelectionCase) error {
	if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Task) == "" {
		return errors.New("Tool selection case requires ID and task")
	}
	switch item.Expected.Decision {
	case "no_tool":
		if item.Expected.Tool != "" || len(item.Expected.Arguments) != 0 || item.Expected.Outcome != "no_tool" {
			return errors.New("no-Tool expectation cannot declare Tool arguments or execution outcome")
		}
	case "tool":
		if strings.TrimSpace(item.Expected.Tool) == "" || len(item.Expected.Arguments) == 0 || strings.TrimSpace(item.Expected.Outcome) == "" {
			return errors.New("Tool expectation requires Tool, arguments, and outcome")
		}
		switch item.Expected.Outcome {
		case "success", string(tools.ErrorInvalidArgs), string(tools.ErrorExecutionFailed):
		default:
			return fmt.Errorf("unsupported Tool outcome %q", item.Expected.Outcome)
		}
	default:
		return fmt.Errorf("unsupported selection decision %q", item.Expected.Decision)
	}
	return nil
}

func ensureSelectionDatasetEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("Tool selection dataset contains trailing JSON")
		}
		return fmt.Errorf("decode trailing Tool selection data: %w", err)
	}
	return nil
}
