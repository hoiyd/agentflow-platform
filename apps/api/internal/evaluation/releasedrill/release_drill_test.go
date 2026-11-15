package releasedrill

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type drill struct {
	options      Options
	report       Report
	apiRoot      string
	workspace    string
	agentID      string
	serverBin    string
	routeFile    string
	toolFile     string
	tempDir      string
	port         string
	provider     *modelStub
	providerHTTP *httptest.Server
	client       *http.Client
	process      *serverProcess
}

func Run(ctx context.Context, options Options) (report Report, resultErr error) {
	if strings.TrimSpace(options.DatabaseURL) == "" {
		return Report{}, errors.New("RELEASE_DRILL_DATABASE_URL or TEST_DATABASE_URL is required; use a disposable PostgreSQL database")
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	root, err := filepath.Abs(options.RepoRoot)
	if err != nil {
		return Report{}, err
	}
	options.RepoRoot = root
	if strings.TrimSpace(options.ReportPath) == "" {
		options.ReportPath = filepath.Join(root, ".cache", "release-drill", "latest.json")
	}

	revision, err := commandOutput(ctx, root, "git", "rev-parse", "HEAD")
	if err != nil {
		return Report{}, fmt.Errorf("read Git revision: %w", err)
	}
	status, err := commandOutput(ctx, root, "git", "status", "--porcelain", "--untracked-files=all", "--", ".", ":(exclude)docs/private")
	if err != nil {
		return Report{}, fmt.Errorf("read Git status: %w", err)
	}
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	d := &drill{
		options: options, workspace: "release_drill_" + suffix, agentID: "agent_release_drill_" + suffix,
		client: &http.Client{},
		report: Report{
			SchemaVersion: reportSchema, Status: "running", GitRevision: strings.TrimSpace(revision),
			WorkingTreeClean: strings.TrimSpace(status) == "", GoVersion: runtime.Version(),
			StartedAt: time.Now().UTC(), Scope: "single_instance_local", WorkspaceID: "release_drill_" + suffix,
			RunIDs: map[string]string{}, Deferred: []DeferredItem{
				{Name: "backup_restore", Reason: "requires a deployment-owned backup target and restore database"},
				{Name: "previous_binary_forward_read", Reason: "requires a retained previous release binary and an explicit compatibility matrix"},
				{Name: "migration_rollback", Reason: "startup migrations are exercised; rollback policy remains deployment-specific"},
			},
		},
	}
	defer func() {
		if d.process != nil {
			d.process.kill()
		}
		if d.providerHTTP != nil {
			d.providerHTTP.Close()
		}
		if d.tempDir != "" {
			_ = os.RemoveAll(d.tempDir)
		}
		d.report.CompletedAt = time.Now().UTC()
		if resultErr == nil {
			d.report.Status = "passed"
		} else {
			d.report.Status = "failed"
		}
		if writeErr := writeReport(options.ReportPath, d.report); writeErr != nil {
			resultErr = errors.Join(resultErr, writeErr)
		}
		report = d.report
	}()

	if err := d.prepare(ctx); err != nil {
		return d.report, err
	}
	if err := d.run(ctx); err != nil {
		return d.report, err
	}
	return d.report, nil
}

func (d *drill) run(ctx context.Context) error {
	if err := d.step(ctx, "startup_dependency_failure", "agentflow-server with DATABASE_URL unset", "process exits before readiness and reports the missing Postgres dependency", func() (string, map[string]any, error) {
		process, err := d.startServer("")
		if err != nil {
			return "", nil, err
		}
		exitErr := process.wait(5 * time.Second)
		if exitErr == nil {
			process.kill()
			return "process unexpectedly stayed available", nil, errors.New("server accepted an empty DATABASE_URL")
		}
		logs := safeText(tail(process.logs.String(), 1200))
		if !strings.Contains(logs, "DATABASE_URL is required") {
			return "process exited without the expected dependency error", map[string]any{"logs": logs}, errors.New("missing DATABASE_URL failure was not explicit")
		}
		return "non-zero exit before readiness", map[string]any{"logs": logs}, nil
	}); err != nil {
		return err
	}

	if err := d.startHealthyServer(ctx, "initial_readiness"); err != nil {
		return err
	}
	if err := d.createWorkspace(ctx); err != nil {
		return err
	}
	if err := d.createAgent(ctx); err != nil {
		return err
	}

	var drainedRun string
	if err := d.step(ctx, "inflight_request_drain", "SIGTERM agentflow-server while an Autonomous model call is in flight", "accepted Run completes and process exits cleanly", func() (string, map[string]any, error) {
		started := d.provider.delayNext(350 * time.Millisecond)
		session, err := d.startAutonomousRun(ctx, "Complete the release drain drill and return a concise result.")
		if err != nil {
			return "", nil, err
		}
		drainedRun = session.runID
		d.report.RunIDs["drained"] = drainedRun
		if err := waitSignal(ctx, started, 10*time.Second, "model request"); err != nil {
			return "model request was not reached", map[string]any{"run_id": drainedRun, "logs": safeText(tail(d.process.logs.String(), 1800))}, err
		}
		if err := d.process.terminate(); err != nil {
			return "", nil, err
		}
		if err := d.process.wait(15 * time.Second); err != nil {
			return "process did not drain cleanly", map[string]any{"run_id": drainedRun, "logs": safeText(tail(d.process.logs.String(), 1600))}, err
		}
		if err := session.wait(2 * time.Second); err != nil {
			return "HTTP stream did not finish cleanly", map[string]any{"run_id": drainedRun}, err
		}
		d.process = nil
		return "process exited 0 after the accepted Run finished", map[string]any{"run_id": drainedRun}, nil
	}); err != nil {
		return err
	}

	if err := d.startHealthyServer(ctx, "restart_after_drain"); err != nil {
		return err
	}
	if err := d.step(ctx, "drained_run_persisted", "GET /api/runs/{drained_run}/replay", "the completed Run is readable after process restart", func() (string, map[string]any, error) {
		replay, err := d.replay(ctx, drainedRun)
		if err != nil {
			return "", nil, err
		}
		if replay.Run.Status != "completed" {
			return "status=" + replay.Run.Status, map[string]any{"run_id": drainedRun}, fmt.Errorf("drained run status is %q", replay.Run.Status)
		}
		return "status=completed", map[string]any{"run_id": drainedRun, "event_count": len(replay.RunEvents)}, nil
	}); err != nil {
		return err
	}

	var crashedRun string
	if err := d.step(ctx, "worker_crash", "SIGKILL agentflow-server while a Stage checkpoint is executing", "process exits abruptly and leaves a durable running Run", func() (string, map[string]any, error) {
		started := d.provider.blockNext()
		session, err := d.startAutonomousRun(ctx, "Complete the crash recovery drill from the durable checkpoint.")
		if err != nil {
			return "", nil, err
		}
		crashedRun = session.runID
		d.report.RunIDs["crashed"] = crashedRun
		if err := waitSignal(ctx, started, 10*time.Second, "blocked model request"); err != nil {
			return "blocked model request was not reached", map[string]any{"run_id": crashedRun, "logs": safeText(tail(d.process.logs.String(), 1800))}, err
		}
		d.process.kill()
		_ = d.process.wait(5 * time.Second)
		_ = session.wait(2 * time.Second)
		d.process = nil
		time.Sleep(250 * time.Millisecond)
		return "process killed after checkpointed Stage start", map[string]any{"run_id": crashedRun}, nil
	}); err != nil {
		return err
	}

	if err := d.startHealthyServer(ctx, "restart_and_stale_repair"); err != nil {
		return err
	}
	if err := d.step(ctx, "stale_run_repair", "GET /api/runs/{crashed_run}/replay", "startup repair marks the stale Run failed_recoverable with checkpoint evidence", func() (string, map[string]any, error) {
		replay, err := d.replay(ctx, crashedRun)
		if err != nil {
			return "", nil, err
		}
		if err := validateRecoverableReplay(replay); err != nil {
			return "recovery evidence incomplete", map[string]any{
				"run_id": crashedRun, "checkpoint_statuses": checkpointStatuses(replay), "event_types": eventTypes(replay),
				"server_logs": safeText(tail(d.process.logs.String(), 1800)),
			}, err
		}
		return "status=failed_recoverable", map[string]any{
			"run_id": crashedRun, "checkpoint_statuses": checkpointStatuses(replay), "event_types": eventTypes(replay),
		}, nil
	}); err != nil {
		return err
	}

	if err := d.step(ctx, "checkpoint_resume", "POST /api/runs/{crashed_run}/resume", "Resume compensates the interrupted Stage and completes the original Run", func() (string, map[string]any, error) {
		status, body, err := d.request(ctx, http.MethodPost, "/api/runs/"+crashedRun+"/resume", `{"user_input":"Resume after the release drill crash."}`)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusOK || !bytes.Contains(body, []byte("event: done")) {
			return fmt.Sprintf("http_status=%d", status), map[string]any{"response": safeText(tail(string(body), 1200))}, errors.New("resume did not complete")
		}
		replay, err := d.replay(ctx, crashedRun)
		if err != nil {
			return "", nil, err
		}
		if err := validateCompletedReplay(replay); err != nil {
			return "resume evidence incomplete", map[string]any{"run_id": crashedRun}, err
		}
		return "status=completed", map[string]any{
			"run_id": crashedRun, "checkpoint_statuses": checkpointStatuses(replay), "event_types": eventTypes(replay),
		}, nil
	}); err != nil {
		return err
	}

	if err := d.step(ctx, "duplicate_resume_rejected", "POST /api/runs/{completed_run}/resume", "a repeated Resume fails with HTTP 409 and does not rerun work", func() (string, map[string]any, error) {
		status, body, err := d.request(ctx, http.MethodPost, "/api/runs/"+crashedRun+"/resume", `{}`)
		if err != nil {
			return "", nil, err
		}
		if status != http.StatusConflict {
			return fmt.Sprintf("http_status=%d", status), map[string]any{"response": safeText(string(body))}, errors.New("duplicate Resume was not rejected")
		}
		return "http_status=409", map[string]any{"run_id": crashedRun}, nil
	}); err != nil {
		return err
	}

	return d.step(ctx, "uncertain_tool_effect_guard", "go test ./internal/httpapi -run ^TestResumeRunRejectsStaleAndUnreconciledActions$ -count=1", "an unresolved external Tool effect blocks Resume", func() (string, map[string]any, error) {
		cmd := exec.CommandContext(ctx, "go", "test", "./internal/httpapi", "-run", "^TestResumeRunRejectsStaleAndUnreconciledActions$", "-count=1")
		cmd.Dir = filepath.Join(d.options.RepoRoot, "apps", "api")
		output, err := cmd.CombinedOutput()
		return "focused contract test executed", map[string]any{"evidence_source": "automated_contract_test", "output": safeText(tail(string(output), 1200))}, err
	})
}
