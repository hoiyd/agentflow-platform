package domain

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
