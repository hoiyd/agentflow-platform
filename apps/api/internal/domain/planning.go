package domain

import "time"

// Planning is a frozen execution restriction, not an orchestration mode or grant.
const ExecutionPlanOnly = "plan_only"

type PlanOrigin struct {
	RunID string `json:"run_id"`
	Hash  string `json:"hash"`
}

type PlanSource struct {
	Root         string `json:"root"`
	Path         string `json:"path"`
	RootRevision string `json:"root_revision"`
	FileID       string `json:"file_id"`
	ContentHash  string `json:"content_hash"`
}

type RunPlan struct {
	Version   string       `json:"version"`
	RunID     string       `json:"run_id"`
	Input     string       `json:"input"`
	Proposal  string       `json:"proposal"`
	Sources   []PlanSource `json:"sources"`
	ExpiresAt time.Time    `json:"expires_at"`
	Hash      string       `json:"hash"`
}
