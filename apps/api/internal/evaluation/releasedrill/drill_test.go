package releasedrill

import (
	"context"
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
