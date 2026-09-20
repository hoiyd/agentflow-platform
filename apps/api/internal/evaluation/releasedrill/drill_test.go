package releasedrill

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestReleaseRecoveryDrill(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("RELEASE_DRILL_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("set RELEASE_DRILL_DATABASE_URL through make release-recovery-drill")
	}
	repoRoot := strings.TrimSpace(os.Getenv("RELEASE_DRILL_REPO_ROOT"))
	if repoRoot == "" {
		t.Fatal("RELEASE_DRILL_REPO_ROOT is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	report, err := Run(ctx, Options{
		RepoRoot: repoRoot, DatabaseURL: databaseURL,
		ReportPath: strings.TrimSpace(os.Getenv("RELEASE_DRILL_REPORT_PATH")), Output: os.Stdout,
	})
	if err != nil {
		t.Fatalf("release recovery drill failed: %v", err)
	}
	if report.Status != "passed" {
		t.Fatalf("release recovery drill status = %q", report.Status)
	}
}

func TestReplayEvidenceValidation(t *testing.T) {
	recoverable := replay{}
	recoverable.Run.Status = "failed_recoverable"
	recoverable.StageCheckpoints = append(recoverable.StageCheckpoints, struct {
		Status string `json:"status"`
	}{Status: "executing"})
	recoverable.RecoverySummary = map[string]any{"reason": "run_recoverable"}
	recoverable.RunEvents = append(recoverable.RunEvents,
		struct {
			Type string `json:"type"`
		}{Type: "stage.failed"},
		struct {
			Type string `json:"type"`
		}{Type: "run.failed"},
	)
	if err := validateRecoverableReplay(recoverable); err != nil {
		t.Fatalf("valid recoverable Replay rejected: %v", err)
	}
	recoverable.StageCheckpoints = nil
	if err := validateRecoverableReplay(recoverable); err == nil {
		t.Fatal("missing checkpoint evidence accepted")
	}

	completed := replay{}
	completed.Run.Status = "completed"
	for _, eventType := range []string{"run.resumed", "checkpoint.compensation_completed", "run.completed"} {
		completed.RunEvents = append(completed.RunEvents, struct {
			Type string `json:"type"`
		}{Type: eventType})
	}
	if err := validateCompletedReplay(completed); err != nil {
		t.Fatalf("valid completed Replay rejected: %v", err)
	}
	completed.RunEvents = completed.RunEvents[:2]
	if err := validateCompletedReplay(completed); err == nil {
		t.Fatal("incomplete Resume evidence accepted")
	}
}

func TestModelStubBlocksOneRequestAndRecovers(t *testing.T) {
	stub := newModelStub()
	server := httptest.NewServer(stub)
	defer server.Close()

	started := stub.blockNext()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/chat/completions",
			strings.NewReader(`{"messages":[{"content":"observe"}]}`))
		request.Header.Set("Content-Type", "application/json")
		_, err := http.DefaultClient.Do(request)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stub did not block the armed request")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not stop after cancellation")
	}

	response, err := http.Post(server.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"messages":[{"content":"observe"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stub did not recover: status=%d", response.StatusCode)
	}
}
