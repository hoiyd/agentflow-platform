package domain

// Ownership describes who may declare a field, not who verified its truth.
// Extensions (including access_scope) never grant permissions.
type DocumentMetadataField struct {
	Key       string   `json:"key"`
	Type      string   `json:"type"`
	Owner     string   `json:"owner"`
	Scope     string   `json:"scope"`
	Nullable  bool     `json:"nullable"`
	MaxLength int      `json:"max_length,omitempty"`
	MaxItems  int      `json:"max_items,omitempty"`
	Values    []string `json:"values,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
}

type DocumentMetadataSchema struct {
	RuntimeFiltering bool                    `json:"runtime_filtering"`
	Version          string                  `json:"version"`
	Fields           []DocumentMetadataField `json:"fields"`
	ExtensionPolicy  string                  `json:"extension_policy"`
	TypedFiltering   bool                    `json:"typed_filtering"`
}
