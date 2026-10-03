package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The CLI fixture is a controlled process, never a substitute for the opt-in
// live VM gate. These cases verify argv/env, exit propagation, output bounds,
// transport cancellation and persisted cleanup state through real os/exec.
func TestCLIProcessFailuresAndOwnershipRecovery(t *testing.T) {
	for _, scenario := range []string{"success", "nonzero", "timeout", "create-failed", "cleanup-failed", "absent-after-failed-create"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			executable := filepath.Join(root, "sbx-fixture")
			namePath := filepath.Join(root, "name")
			script := "#!/bin/sh\ncase \"$1\" in\ncreate) printf '%s' \"$3\" > '" + namePath + "'\n"
			if strings.Contains(scenario, "create") {
				script += "exit 2\n"
			}
			script += ";;\nexec)\n[ -z \"$OPENAI_API_KEY$SSH_AUTH_SOCK$HTTP_PROXY\" ] || exit 99\n"
			switch scenario {
			case "nonzero":
				script += "printf command-failed; exit 7\n"
			case "timeout":
				script += "exec /bin/sleep 60\n"
			default:
				script += "printf 'abcdefghijklmnopqrstuvwxyz\\000'\n"
			}
			script += ";;\nrm)\n"
			if scenario == "cleanup-failed" || scenario == "absent-after-failed-create" {
				script += "exit 3\n"
			}
			script += ";;\nls)\n"
			if scenario == "cleanup-failed" {
				script += "/bin/cat '" + namePath + "'\n"
			}
			script += ";;\nesac\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("OPENAI_API_KEY", "fixture-only")
			t.Setenv("SSH_AUTH_SOCK", "/fixture/ssh")
			t.Setenv("HTTP_PROXY", "http://fixture.invalid")
			options := Options{Executable: executable, StateDirectory: filepath.Join(root, "state"), AllowedCommands: []string{"/bin/sh"}, Timeout: time.Second, MaxOutputBytes: 20}
			r, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Recover(t.Context()); err != nil {
				t.Fatal(err)
			}
			result, err := r.Run(t.Context(), []string{"/bin/sh"})
			switch scenario {
			case "success":
				if err != nil || result.ExitCode != 0 || result.Output != "abcdefghijklmnopqrst" || result.ObservedBytes != 27 || !result.Truncated || !result.CleanupConfirmed {
					t.Fatalf("receipt: %+v %v", result, err)
				}
			case "nonzero":
				if err != nil || result.ExitCode != 7 || !result.CleanupConfirmed {
					t.Fatalf("exit: %+v %v", result, err)
				}
			case "timeout":
				if !errors.Is(err, context.DeadlineExceeded) || !result.CleanupConfirmed {
					t.Fatalf("timeout: %+v %v", result, err)
				}
			case "create-failed", "absent-after-failed-create":
				var unavailable *Error
				if !errors.As(err, &unavailable) || unavailable.Kind != Unavailable || !result.CleanupConfirmed {
					t.Fatalf("create failure: %+v %v", result, err)
				}
			case "cleanup-failed":
				var uncertain *Error
				if !errors.As(err, &uncertain) || uncertain.Kind != CleanupFailed || result.CleanupConfirmed {
					t.Fatalf("cleanup: %+v %v", result, err)
				}
			}
			if err := r.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Run(t.Context(), []string{"/bin/sh"}); err == nil {
				t.Fatal("closed runner accepted work")
			}
			if scenario == "cleanup-failed" {
				// Restart against the same directory. Recovery must fail closed while
				// the VM is still reported present, then remove it after a real ack.
				restarted, err := New(options)
				if err != nil {
					t.Fatal(err)
				}
				defer restarted.Close(context.Background())
				if err := restarted.Recover(t.Context()); err == nil {
					t.Fatal("uncertain cleanup accepted at startup")
				}
				if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := restarted.Recover(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStateOwnershipLockAndUntrustedRecords(t *testing.T) {
	root := t.TempDir()
	options := Options{Executable: "/usr/bin/true", StateDirectory: filepath.Join(root, "state"), AllowedCommands: []string{"/bin/sh"}}
	r, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	if other, err := New(options); err == nil {
		_ = other.Close(t.Context())
		t.Fatal("two API owners accepted")
	}
	if err := os.WriteFile(filepath.Join(options.StateDirectory, "someone-elses-sandbox"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.Recover(t.Context()); err == nil {
		t.Fatal("unexpected ownership entries were ignored")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(options.StateDirectory, link); err != nil {
		t.Fatal(err)
	}
	options.StateDirectory = link
	if other, err := New(options); err == nil {
		_ = other.Close(t.Context())
		t.Fatal("symlinked state directory accepted")
	}
}
