package releasedrill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const reportSchema = "agentflow-release-recovery-drill-v1"

type Options struct {
	RepoRoot    string
	DatabaseURL string
	ReportPath  string
	Output      io.Writer
}

type Report struct {
	SchemaVersion    string            `json:"schema_version"`
	Status           string            `json:"status"`
	GitRevision      string            `json:"git_revision"`
	WorkingTreeClean bool              `json:"working_tree_clean"`
	GoVersion        string            `json:"go_version"`
	StartedAt        time.Time         `json:"started_at"`
	CompletedAt      time.Time         `json:"completed_at"`
	Scope            string            `json:"scope"`
	WorkspaceID      string            `json:"workspace_id"`
	RunIDs           map[string]string `json:"run_ids"`
	Steps            []Step            `json:"steps"`
	Deferred         []DeferredItem    `json:"deferred"`
}

type Step struct {
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Command    string         `json:"command"`
	Expected   string         `json:"expected"`
	Observed   string         `json:"observed"`
	DurationMS int64          `json:"duration_ms"`
	Evidence   map[string]any `json:"evidence,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type DeferredItem struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func (d *drill) step(ctx context.Context, name, command, expected string, run func() (string, map[string]any, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	start := time.Now()
	observed, evidence, err := run()
	item := Step{Name: name, Status: "passed", Command: command, Expected: expected, Observed: observed,
		DurationMS: time.Since(start).Milliseconds(), Evidence: evidence}
	if err != nil {
		item.Status, item.Error = "failed", safeText(err.Error())
	}
	d.report.Steps = append(d.report.Steps, item)
	_, _ = fmt.Fprintf(d.options.Output, "[%s] %s: %s\n", item.Status, name, observed)
	if writeErr := writeReport(d.options.ReportPath, d.report); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func writeReport(path string, report Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(data, '\n'), 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
