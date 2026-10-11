package domain

import "fmt"

// ToolSchemaConfig controls model visibility, never execution authority. It is
// frozen with a Run; a missing config in existing snapshots means eager.
type ToolSchemaConfig struct {
	Mode                 string `json:"mode"`
	SchemaTokenThreshold int    `json:"schema_token_threshold"`
}

func (c ToolSchemaConfig) Normalize() ToolSchemaConfig {
	if c.Mode == "" {
		c.Mode = "eager"
	}
	if c.SchemaTokenThreshold == 0 {
		c.SchemaTokenThreshold = 2048
	}
	return c
}

func (c ToolSchemaConfig) Validate() error {
	switch c.Mode {
	case "eager", "lazy", "auto":
	default:
		return fmt.Errorf("invalid Tool Schema mode %q", c.Mode)
	}
	if c.SchemaTokenThreshold < 1 {
		return fmt.Errorf("Tool Schema token threshold must be positive")
	}
	return nil
}

type ToolSchemaIdentity struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

// ToolDiscoveryState is a bounded read model of the visible set for one
// Run/Stage/Agent. It never replaces the immutable authorized candidate set.
type ToolDiscoveryState struct {
	AgentID                      string               `json:"agent_id"`
	Mode                         string               `json:"mode"`
	CandidateDigest              string               `json:"candidate_digest"`
	Active                       []ToolSchemaIdentity `json:"active"`
	SearchCalls                  int                  `json:"search_calls"`
	Reason                       string               `json:"reason"`
	FullSchemaEstimatedTokens    int                  `json:"full_schema_estimated_tokens"`
	VisibleSchemaEstimatedTokens int                  `json:"visible_schema_estimated_tokens"`
}
