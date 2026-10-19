package httpapi

import (
	"encoding/json"
	"fmt"

	"agentflow-platform/apps/api/internal/rag/eligibility"
)

// Struct decoding loses absent-vs-null group members and accepts case aliases.
// Check the small filter envelope first so malformed constraints never disappear.
func validateMetadataFilterJSON(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if len(raw) > eligibility.MaxBytes || json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["schema_version"] == nil || fields["where"] == nil {
		return fmt.Errorf("%w: expected schema_version and where within 16 KiB", eligibility.ErrInvalid)
	}
	nodes := 0
	return validateMetadataConditionJSON(fields["where"], 1, &nodes)
}

func validateMetadataConditionJSON(raw json.RawMessage, depth int, nodes *int) error {
	*nodes = *nodes + 1
	var fields map[string]json.RawMessage
	if depth > eligibility.MaxDepth || *nodes > eligibility.MaxNodes || json.Unmarshal(raw, &fields) != nil || len(fields) == 0 {
		return fmt.Errorf("%w: invalid or oversized condition", eligibility.ErrInvalid)
	}
	group := fields["all"]
	if fields["any"] != nil {
		group = fields["any"]
	}
	if group != nil {
		var children []json.RawMessage
		if len(fields) != 1 || json.Unmarshal(group, &children) != nil || len(children) == 0 {
			return fmt.Errorf("%w: expected one nonempty all or any group", eligibility.ErrInvalid)
		}
		for _, child := range children {
			if err := validateMetadataConditionJSON(child, depth+1, nodes); err != nil {
				return err
			}
		}
		return nil
	}
	if len(fields) != 3 || fields["field"] == nil || fields["op"] == nil || fields["value"] == nil {
		return fmt.Errorf("%w: expected field, op and value", eligibility.ErrInvalid)
	}
	return nil
}
