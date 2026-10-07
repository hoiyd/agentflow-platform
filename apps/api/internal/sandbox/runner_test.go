package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New(Options{Executable: "/usr/bin/true", StateDirectory: filepath.Join(t.TempDir(), "state"), AllowedCommands: []string{"/bin/sh"}, Timeout: time.Second, MaxOutputBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(context.Background()) })
	return r
}

func TestRunnerUsesMountlessDeniedNetworkAndAlwaysRemoves(t *testing.T) {
	r := testRunner(t)
	var calls [][]string
	r.invoke = func(ctx context.Context, args []string, output *outputBuffer) error {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "exec" {
			_, _ = output.Write([]byte("hello\x00world"))
		}
		return nil
	}
	result, err := r.Run(context.Background(), []string{"/bin/sh", "-c", "printf hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || calls[0][0] != "create" || calls[1][0] != "exec" || calls[2][0] != "rm" {
		t.Fatalf("lifecycle: %v", calls)
	}
	create := strings.Join(calls[0], " ")
	if !strings.Contains(create, "--deny-network **") || !strings.HasSuffix(create, " shell") || strings.Contains(create, "--env") {
		t.Fatalf("unsafe create: %s", create)
	}
	if !strings.Contains(create, "--skills off") {
		t.Fatal("sbx must not share host skills by default")
	}
	if !strings.Contains(strings.Join(calls[1], " "), "--user 65534:65534") {
		t.Fatal("guest must not run as root or agent")
	}
	if !strings.Contains(strings.Join(calls[1], " "), "/usr/bin/setpriv --no-new-privs") {
		t.Fatal("guest must not gain privileges through setuid tools")
	}
	if !reflect.DeepEqual(calls[1][len(calls[1])-3:], []string{"/bin/sh", "-c", "printf hello"}) {
		t.Fatalf("argv was interpreted by host: %v", calls[1])
	}
	if result.ExitCode != 0 || result.SandboxID == "" || result.PolicyRevision != r.Revision() || strings.ContainsRune(result.Output, 0) || !result.CleanupConfirmed {
		t.Fatalf("receipt: %+v", result)
	}
	entries, _ := os.ReadDir(r.options.StateDirectory)
	if len(entries) != 1 || entries[0].Name() != ".lock" {
		t.Fatalf("ownership records not cleared: %v", entries)
	}
}

func TestRunnerReportsRealPhasesWithoutOutput(t *testing.T) {
	r := testRunner(t)
	var phases []string
	r.invoke = func(_ context.Context, args []string, output *outputBuffer) error {
		if args[0] == "exec" {
			_, _ = output.Write([]byte("private stdout"))
		}
		return nil
	}
	result, err := r.RunWithProgress(context.Background(), []string{"/bin/sh"}, func(phase string) { phases = append(phases, phase) })
	if err != nil || !result.CleanupConfirmed || !reflect.DeepEqual(phases, []string{"provisioning", "executing", "cleaning_up", "cleanup_confirmed"}) {
		t.Fatalf("phases=%v result=%#v err=%v", phases, result, err)
	}
}

func TestRunnerRejectsUnsafeArgumentsWithoutStartingCLI(t *testing.T) {
	r := testRunner(t)
	r.invoke = func(context.Context, []string, *outputBuffer) error { t.Fatal("rejected command ran"); return nil }
	for _, args := range [][]string{nil, {"sh"}, {"/bin/other"}, {"/bin/sh", "bad\x00arg"}, {"/bin/sh", strings.Repeat("x", 32769)}} {
		_, err := r.Run(context.Background(), args)
		var denied *Error
		if !errors.As(err, &denied) || denied.Kind != Denied {
			t.Fatalf("expected denial for %v, got %v", args, err)
		}
	}
}

func TestRunnerFailurePathsAndCleanup(t *testing.T) {
	for _, stage := range []string{"create", "exec", "rm"} {
		t.Run(stage, func(t *testing.T) {
			r := testRunner(t)
			var executed, removed bool
			r.invoke = func(_ context.Context, args []string, _ *outputBuffer) error {
				if args[0] == "exec" {
					executed = true
				}
				if args[0] == "rm" {
					removed = true
				}
				if stage == "rm" && args[0] == "ls" {
					return errors.New("inventory unavailable")
				}
				if args[0] == stage {
					return errors.New("injected fault containing secret")
				}
				return nil
			}
			_, err := r.Run(context.Background(), []string{"/bin/sh"})
			if err == nil || strings.Contains(err.Error(), "secret") || !removed {
				t.Fatalf("failure/cleanup: %v removed=%v", err, removed)
			}
			if stage == "create" && executed {
				t.Fatal("executed after failed creation")
			}
			if stage == "rm" {
				_, err = r.Run(context.Background(), []string{"/bin/sh"})
				var blocked *Error
				if !errors.As(err, &blocked) || blocked.Kind != CleanupFailed {
					t.Fatalf("did not fail closed: %v", err)
				}
			}
		})
	}
}

func TestRunnerCancellationAndCapacity(t *testing.T) {
	r := testRunner(t)
	started := make(chan struct{})
	r.invoke = func(ctx context.Context, args []string, _ *outputBuffer) error {
		if args[0] == "exec" {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}
		if args[0] == "rm" && ctx.Err() != nil {
			t.Error("cleanup inherited canceled context")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := r.Run(ctx, []string{"/bin/sh"}); finished <- err }()
	<-started
	_, err := r.Run(context.Background(), []string{"/bin/sh"})
	var capacity *Error
	if !errors.As(err, &capacity) || capacity.Kind != Busy {
		t.Fatalf("capacity: %v", err)
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestRecoveryRemovesOnlyRecordedSandboxes(t *testing.T) {
	r := testRunner(t)
	name := "agentflow-0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(r.options.StateDirectory, name), []byte(name), 0600); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	r.invoke = func(_ context.Context, args []string, _ *outputBuffer) error { calls = append(calls, args); return nil }
	if err := r.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], []string{"rm", "--force", name}) {
		t.Fatalf("recovery touched other sandboxes: %v", calls)
	}
	if _, err := os.Stat(filepath.Join(r.options.StateDirectory, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("record survived confirmed cleanup")
	}
}

func TestOptionsAndRevision(t *testing.T) {
	for _, options := range []Options{{}, {Executable: "sbx", StateDirectory: t.TempDir()}, {Executable: "/usr/bin/true", StateDirectory: t.TempDir(), AllowedCommands: []string{"sh"}}} {
		if r, err := New(options); err == nil {
			_ = r.Close(context.Background())
			t.Fatalf("invalid options accepted: %+v", options)
		}
	}
	r := testRunner(t)
	other := testRunner(t)
	if r.Revision() != other.Revision() {
		t.Fatal("local state path must not affect frozen policy")
	}
	other.options.CPUs++
	if r.Revision() == other.Revision() {
		t.Fatal("policy changes must change revision")
	}
}

func TestAvailabilityRejectsUnsupportedCLIAndUnavailableDaemon(t *testing.T) {
	r := testRunner(t)
	for _, scenario := range []string{"old-cli", "no-login", "ready"} {
		r.invoke = func(_ context.Context, args []string, output *outputBuffer) error {
			if args[0] == "create" && scenario != "old-cli" {
				_, _ = output.Write([]byte("--skills --deny-network --cpus --memory --template"))
			}
			if args[0] == "ls" && scenario == "no-login" {
				return errors.New("not authenticated")
			}
			return nil
		}
		if err := r.CheckAvailability(t.Context()); (err == nil) != (scenario == "ready") {
			t.Fatalf("availability %s: %v", scenario, err)
		}
	}
}
