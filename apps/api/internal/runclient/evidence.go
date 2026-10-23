package runclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"runtime/debug"
	"time"

	"agentflow-platform/apps/api/internal/apicontract"
)

type Report struct {
	Schema         string                `json:"schema"`
	GeneratedAt    time.Time             `json:"generated_at"`
	ClientIdentity map[string]string     `json:"client_identity"`
	APIReplayURL   string                `json:"api_replay_url"`
	ReplayPath     string                `json:"replay_path"`
	ReplayURL      string                `json:"replay_url,omitempty"`
	RequestSHA256  string                `json:"request_sha256,omitempty"`
	After          int64                 `json:"after"`
	Replay         apicontract.RunReplay `json:"replay"`
	Limitations    []string              `json:"limitations"`
}

func digest(value []byte) string { hash := sha256.Sum256(value); return hex.EncodeToString(hash[:]) }

func (c *Client) Result(ctx context.Context, id string) (Report, error) {
	path, err := runPath(id, "/replay")
	if err != nil {
		return Report{}, err
	}
	report := Report{Schema: "agentflow-headless-result-v1", GeneratedAt: time.Now().UTC(), APIReplayURL: c.options.APIURL + path, ReplayPath: "/runs/" + id, ClientIdentity: buildIdentity(), Limitations: []string{"evidence contains authorized conversation/context content; keep it private", "durable events are authoritative; stream-only deltas cannot be replayed", "client disconnect/deadline does not stop a Run; cancel is explicit"}}
	if c.options.WebURL != "" {
		report.ReplayURL = c.options.WebURL + report.ReplayPath
	}
	if err = c.get(ctx, path, &report.Replay); err != nil {
		return Report{}, err
	}
	if report.Replay.Run.Id != id || report.Replay.Run.WorkspaceId != c.options.Workspace {
		return Report{}, problem("protocol_error", "Replay identity mismatch")
	}
	report.After = report.Replay.Projection.AsOfSequence
	// Replay includes the same H-51 read model as /result, coherently with the
	// evidence and Ledger. Never re-parse a message as a verified machine value.
	if result := report.Replay.StructuredResult; result != nil {
		if result.RunId != id || result.Status != report.Replay.Run.Status || result.VerificationStatus != report.Replay.Run.VerificationStatus {
			return Report{}, problem("protocol_error", "structured result identity/status mismatch")
		}
		if result.Value != nil && (result.Status != "completed" || result.VerificationStatus != "passed") {
			return Report{}, problem("protocol_error", "unaccepted structured value")
		}
	}
	return report, nil
}

func buildIdentity() map[string]string {
	identity := map[string]string{"revision": "unknown"}
	if info, ok := debug.ReadBuildInfo(); ok {
		identity["go_version"] = info.GoVersion
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				identity["revision"] = setting.Value
			case "vcs.modified":
				identity["modified"] = setting.Value
			}
		}
	}
	return identity
}

func saveReport(path string, report Report) error {
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return problem("output_failed", "cannot create evidence file; use a new path in an existing directory")
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(path)
		}
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(report); err != nil {
		return problem("output_failed", "cannot write evidence file")
	}
	if err = file.Sync(); err != nil {
		return problem("output_failed", "cannot sync evidence file")
	}
	if err = file.Close(); err != nil {
		return problem("output_failed", "cannot close evidence file")
	}
	complete = true
	return nil
}

func emit(out io.Writer, value any) error {
	if err := json.NewEncoder(out).Encode(value); err != nil {
		return problem("output_failed", "cannot write stdout")
	}
	return nil
}

func resultExit(report Report) int {
	run := report.Replay.Run
	if run.VerificationStatus == "failed" || run.VerificationStatus == "blocked" || run.Status == "failed" || run.Status == "failed_recoverable" {
		return 3
	}
	switch run.Status {
	case "completed":
		if run.VerificationStatus != "passed" && run.VerificationStatus != "not_required" {
			return 3
		}
		if run.CompletionContract != nil && run.CompletionContract.Output != nil && (report.Replay.StructuredResult == nil || report.Replay.StructuredResult.Value == nil) {
			return 3
		}
		return 0
	case "waiting_for_user":
		return 5
	case "canceled":
		return 6
	default:
		return 5
	}
}
