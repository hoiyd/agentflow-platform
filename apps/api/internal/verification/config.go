package verification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"agentflow-platform/apps/api/internal/domain"
)

// decodeConfig keeps verifier configuration strict while leaving VerifierSpec
// open to new verifier types. Unknown fields are rejected at contract freeze.
func decodeConfig[T any](spec *domain.VerifierSpec) (T, error) {
	var config T
	if spec == nil {
		return config, invalidContract("verifier spec is required")
	}
	encoded, err := json.Marshal(spec.Config)
	if err != nil {
		return config, invalidContract(fmt.Sprintf("verifier %s config is not JSON encodable", spec.ID))
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, invalidContract(fmt.Sprintf("verifier %s has invalid config: %v", spec.ID, err))
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, invalidContract(fmt.Sprintf("verifier %s config has trailing content", spec.ID))
	}
	return config, nil
}

func freezeConfig[T any](spec *domain.VerifierSpec, config T) error {
	encoded, err := json.Marshal(config)
	if err != nil {
		return invalidContract(fmt.Sprintf("verifier %s config cannot be frozen: %v", spec.ID, err))
	}
	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return invalidContract(fmt.Sprintf("verifier %s config cannot be normalized: %v", spec.ID, err))
	}
	spec.Config = normalized
	return nil
}
