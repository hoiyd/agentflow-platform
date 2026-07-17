package verification

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	defaultVerifierTimeoutMS = int64(30_000)
	maxVerifierTimeoutMS     = int64(300_000)
)

func (r *Registry) FreezeContract(input *domain.CompletionContract) (*domain.CompletionContract, error) {
	if input == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(input)
	if err != nil {
		return nil, &VerificationError{Kind: ErrorInvalidContract, Message: "encode completion contract", Cause: err}
	}
	var contract domain.CompletionContract
	if err := json.Unmarshal(bytes, &contract); err != nil {
		return nil, &VerificationError{Kind: ErrorInvalidContract, Message: "decode completion contract", Cause: err}
	}
	contract.ID = strings.TrimSpace(contract.ID)
	if contract.ID == "" {
		contract.ID = newID("contract")
	}
	contract.Version = domain.CurrentCompletionContractVersion
	contract.SubjectType = strings.TrimSpace(contract.SubjectType)
	if contract.SubjectType == "" {
		contract.SubjectType = "run_output"
	}
	if contract.SubjectType != "run_output" {
		return nil, invalidContract("subject_type must be run_output")
	}
	if len(contract.Verifiers) == 0 {
		return nil, invalidContract("at least one verifier is required")
	}
	seen := make(map[string]bool, len(contract.Verifiers))
	required := 0
	for index := range contract.Verifiers {
		spec := &contract.Verifiers[index]
		spec.ID = strings.TrimSpace(spec.ID)
		if spec.ID == "" {
			return nil, invalidContract(fmt.Sprintf("verifiers[%d].id is required", index))
		}
		if seen[spec.ID] {
			return nil, invalidContract("duplicate verifier id: " + spec.ID)
		}
		seen[spec.ID] = true
		verifier, ok := r.Resolve(spec.Type)
		if !ok {
			return nil, invalidContract("unsupported verifier type: " + string(spec.Type))
		}
		spec.Version = verifier.Version()
		if spec.TimeoutMS == 0 {
			spec.TimeoutMS = defaultVerifierTimeoutMS
		}
		if spec.TimeoutMS < 1 || spec.TimeoutMS > maxVerifierTimeoutMS {
			return nil, invalidContract(fmt.Sprintf("verifier %s timeout_ms must be between 1 and %d", spec.ID, maxVerifierTimeoutMS))
		}
		if err := verifier.NormalizeConfig(spec); err != nil {
			return nil, err
		}
		if spec.Required {
			required++
		}
	}
	if required == 0 {
		return nil, invalidContract("at least one verifier must be required")
	}
	if contract.Policy.Mode == "" {
		contract.Policy.Mode = domain.VerificationAllMustPass
	}
	if contract.Policy.Mode != domain.VerificationAllMustPass && contract.Policy.Mode != domain.VerificationAnyMayPass {
		return nil, invalidContract("policy.mode must be all_must_pass or any_may_pass")
	}
	if contract.Policy.MaxAttempts == 0 {
		contract.Policy.MaxAttempts = 2
	}
	if contract.Policy.MaxAttempts < 1 || contract.Policy.MaxAttempts > 5 {
		return nil, invalidContract("policy.max_attempts must be between 1 and 5")
	}
	if contract.Policy.OnExhausted == "" {
		contract.Policy.OnExhausted = domain.VerificationWaitForUser
	}
	if contract.Policy.OnExhausted != domain.VerificationFailRun && contract.Policy.OnExhausted != domain.VerificationWaitForUser {
		return nil, invalidContract("policy.on_exhausted must be fail or waiting_for_user")
	}
	contract.Hash = ""
	contract.Hash, err = completionContractHash(contract)
	if err != nil {
		return nil, &VerificationError{Kind: ErrorInvalidContract, Message: "hash completion contract", Cause: err}
	}
	return &contract, nil
}

func completionContractHash(contract domain.CompletionContract) (string, error) {
	contract.Hash = ""
	return hashJSON(contract)
}

func SubjectForRunOutput(value string) Subject {
	return Subject{Type: "run_output", Value: value, Hash: hashBytes([]byte(value))}
}

func SnapshotHash(snapshot *domain.RuntimeSnapshot) (string, error) {
	if snapshot == nil {
		return hashBytes(nil), nil
	}
	return hashJSON(snapshot)
}

func hashJSON(value any) (string, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return hashBytes(bytes), nil
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func invalidContract(message string) error {
	return &VerificationError{Kind: ErrorInvalidContract, Message: message}
}

func newID(prefix string) string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%s_%x", prefix, sha256.Sum256([]byte(fmt.Sprint(bytes))))[:len(prefix)+1+24]
	}
	return prefix + "_" + hex.EncodeToString(bytes)
}
