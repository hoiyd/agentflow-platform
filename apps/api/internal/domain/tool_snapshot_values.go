package domain

// These values are the durable Tool contract frozen with each Run. Runtime
// policy and progress behavior lives in the Tool packages, not in domain.
type ToolSecuritySource string
type ToolResourceKind string
type ToolResourceAccess string

type ToolResourceScope struct {
	Kind   ToolResourceKind   `json:"kind"`
	Name   string             `json:"name"`
	Access ToolResourceAccess `json:"access"`
}

type ToolNetworkMode string

type ToolNetworkScope struct {
	Mode    ToolNetworkMode `json:"mode"`
	Targets []string        `json:"targets,omitempty"`
}

type ToolScope struct {
	Resources   []ToolResourceScope `json:"resources,omitempty"`
	Network     ToolNetworkScope    `json:"network"`
	Credentials []string            `json:"credential_scopes,omitempty"`
}

type ToolSideEffectClass string
type ToolRateClass string
type ToolReversibility string
type ToolVisibility string
type ToolApprovalMode string
type ToolAuditLevel string

type ToolCapability struct {
	Source        ToolSecuritySource  `json:"source"`
	Scope         ToolScope           `json:"scope"`
	SideEffect    ToolSideEffectClass `json:"side_effect_class"`
	Rate          ToolRateClass       `json:"rate"`
	Reversibility ToolReversibility   `json:"reversibility"`
	Visibility    ToolVisibility      `json:"visibility"`
	Approval      ToolApprovalMode    `json:"approval_mode"`
	Audit         ToolAuditLevel      `json:"audit_level"`
}

type ToolPolicyAction string

type ToolSecurityRule struct {
	ID         string           `json:"id"`
	Tool       string           `json:"tool"`
	Action     ToolPolicyAction `json:"action"`
	Capability ToolCapability   `json:"capability"`
}

type ToolSecurityPolicy struct {
	Version       string             `json:"version"`
	DefaultAction ToolPolicyAction   `json:"default_action"`
	Rules         []ToolSecurityRule `json:"rules,omitempty"`
}

type ToolProgressGuardConfig struct {
	Version    string `json:"version"`
	Enabled    bool   `json:"enabled"`
	WarnAfter  int    `json:"warn_after"`
	BlockAfter int    `json:"block_after"`
	HaltAfter  int    `json:"halt_after"`
	HistoryMax int    `json:"history_max"`
}
