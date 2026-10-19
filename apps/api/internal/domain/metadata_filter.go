package domain

import (
	"bytes"
	"encoding/json"
)

// MetadataFilter is an opt-in typed contract, separate from legacy text equality.
// Schema eligibility and Workspace scope always wrap the entire expression.
type MetadataFilter struct {
	SchemaVersion string            `json:"schema_version"`
	Where         MetadataCondition `json:"where"`
}

// MetadataCondition is exactly one nonempty all/any group or field/op/value leaf.
// Fields explicitly address document.metadata.<key> or chunk.metadata.<key>.
type MetadataCondition struct {
	All   []MetadataCondition `json:"all,omitempty"`
	Any   []MetadataCondition `json:"any,omitempty"`
	Field string              `json:"field,omitempty"`
	Op    string              `json:"op,omitempty"`
	Value any                 `json:"value,omitempty"`
}

// Snapshot/event round-trips must preserve decimal filter boundaries as well
// as request decoding. Default interface decoding would silently use float64.
func (c *MetadataCondition) UnmarshalJSON(data []byte) error {
	type condition MetadataCondition
	var value condition
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*c = MetadataCondition(value)
	return nil
}

// KnowledgeScope is an immutable business restriction, never a frozen grant.
// It lives inside RuntimeSnapshot JSON; no parallel database version is needed.
type KnowledgeScope struct {
	Filter        MetadataFilter `json:"filter"`
	ID            string         `json:"id"`
	PolicyVersion string         `json:"policy_version"`
	Source        string         `json:"source"`
}
