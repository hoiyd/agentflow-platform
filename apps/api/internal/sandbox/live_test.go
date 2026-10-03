package sandbox

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt-in real sbx gate. Never silently replace this with a fake CLI or plain
// container: only a successful run here is evidence of the actual VM boundary.
func TestLiveSBXIsolationAndCleanup(t *testing.T) {
	if os.Getenv("AGENTFLOW_SANDBOX_TEST") != "1" {
		t.Skip("requires authenticated local sbx; set AGENTFLOW_SANDBOX_TEST=1")
	}
	executable, err := exec.LookPath("sbx")
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(Options{Executable: executable, StateDirectory: filepath.Join(t.TempDir(), "state"), AllowedCommands: []string{"/bin/sh", "/usr/bin/python3"}, Timeout: 2 * time.Minute, MaxOutputBytes: 65536})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		if err := r.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := r.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := r.CheckAvailability(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "fixture-host-canary-not-a-real-secret")
	hostPath := filepath.Join(t.TempDir(), "host-canary")
	if err := os.WriteFile(hostPath, []byte("host canary"), 0600); err != nil {
		t.Fatal(err)
	}
	program := `import os, pathlib, socket, json, resource, mmap
assert os.getuid() == 65534
assert os.getenv("OPENAI_API_KEY") is None
assert os.getenv("SSH_AUTH_SOCK") is None
assert "NoNewPrivs:\t1" in pathlib.Path("/proc/self/status").read_text()
assert not pathlib.Path(__import__("sys").argv[1]).exists()
pathlib.Path("proof.txt").write_text("scratch works")
assert pathlib.Path("proof.txt").read_text() == "scratch works"
assert resource.getrlimit(resource.RLIMIT_NPROC)[1] == 64
assert resource.getrlimit(resource.RLIMIT_NOFILE)[1] == 128
assert resource.getrlimit(resource.RLIMIT_CORE)[1] == 0
assert resource.getrlimit(resource.RLIMIT_FSIZE)[1] == 16777216
try:
 mmap.mmap(-1, resource.getrlimit(resource.RLIMIT_AS)[1] + 1)
except (OSError, MemoryError, OverflowError): pass
else: raise AssertionError("address-space limit escaped")
try:
 with open("oversized.bin", "wb", buffering=0) as file:
  for _ in range(17): file.write(b"x" * 1048576)
except OSError: pass
else: raise AssertionError("file-size limit escaped")
handles = []
try:
 for _ in range(256): handles.append(open("/dev/null"))
except OSError: pass
else: raise AssertionError("descriptor limit escaped")
finally:
 for handle in handles: handle.close()
try:
 pathlib.Path(__import__("sys").argv[1]).write_text("overwrite")
except OSError: pass
else: raise AssertionError("host path was writable")
denied = []
for address in [("1.1.1.1", 443), ("169.254.169.254", 80)]:
 try:
  with socket.create_connection(address, timeout=3): pass
 except OSError: denied.append(address[0])
 else: raise AssertionError("network escaped")
print(json.dumps({"scratch_write": True, "host_read_denied": True, "host_write_denied": True, "credentials_absent": True, "no_new_privileges": True, "resource_limits_enforced": True, "network_denied": denied}))`
	result, err := r.Run(t.Context(), []string{"/usr/bin/python3", "-c", program, hostPath})
	if err != nil || result.ExitCode != 0 || !result.CleanupConfirmed {
		t.Fatalf("live isolation: %+v error=%v", result, err)
	}
	var proof map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Output)), &proof); err != nil {
		t.Fatalf("invalid guest evidence: %+v %v", result, err)
	}
	bytes, err := os.ReadFile(hostPath)
	if err != nil || string(bytes) != "host canary" {
		t.Fatal("host canary changed", err)
	}
	large, err := r.Run(t.Context(), []string{"/usr/bin/python3", "-c", "import sys; sys.stdout.write('x'*262144)"})
	if err != nil || large.ExitCode != 0 || !large.Truncated || large.ObservedBytes < 262144 || len(large.Output) > 65536 || !large.CleanupConfirmed {
		t.Fatalf("large output: %+v %v", large, err)
	}
	failed, err := r.Run(t.Context(), []string{"/bin/sh", "-c", "exit 7"})
	if err != nil || failed.ExitCode != 7 || !failed.CleanupConfirmed {
		t.Fatalf("nonzero exit: %+v %v", failed, err)
	}
	// Cancel only after real guest stdout arrives, not after an arbitrary sleep
	// that could merely time out during image download or VM creation.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	actual := r.invoke
	r.invoke = func(ctx context.Context, args []string, output *outputBuffer) error {
		if args[0] != "exec" {
			return actual(ctx, args, output)
		}
		observed := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(observed)
			for {
				output.mu.Lock()
				started := strings.Contains(output.content.String(), "guest-started")
				output.mu.Unlock()
				if started {
					cancel()
					return
				}
				select {
				case <-finished:
					return
				case <-ctx.Done():
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		}()
		err := actual(ctx, args, output)
		close(finished)
		<-observed
		return err
	}
	canceled, err := r.Run(ctx, []string{"/bin/sh", "-c", "printf guest-started; sleep 300 & wait"})
	if ctx.Err() == nil || err == nil || !strings.Contains(canceled.Output, "guest-started") || !canceled.CleanupConfirmed {
		t.Fatalf("guest cancellation: %+v %v", canceled, err)
	}
	r.invoke = actual
	evidence, err := json.MarshalIndent(map[string]any{"schema": "sbx-execution-evidence-v1", "policy_revision": r.Revision(), "isolation": result, "proof": proof, "large_output": map[string]any{"sandbox_id": large.SandboxID, "observed_bytes": large.ObservedBytes, "hash": large.OutputHash, "truncated": large.Truncated, "cleanup_confirmed": large.CleanupConfirmed}, "nonzero": failed, "canceled": canceled, "limitations": []string{"single API owner", "no host mounts or exported files", "no Skill script lifecycle or live model quality"}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("SANDBOX_TEST_EVIDENCE_PATH")
	if path == "" {
		path = filepath.Join(t.TempDir(), "sandbox-execution-evidence.json")
	}
	if err := os.WriteFile(path, evidence, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("sandbox_evidence=%s", path)
}
