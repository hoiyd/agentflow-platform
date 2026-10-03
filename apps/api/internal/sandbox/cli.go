package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// CheckAvailability is a non-executing startup check. It catches unsupported
// CLI safety flags and daemon/auth failures before the Tool is made available;
// it does not assert that an actual VM has passed the live isolation gate.
func (r *Runner) CheckAvailability(ctx context.Context) error {
	output := newOutputBuffer(64 * 1024)
	if err := r.invoke(ctx, []string{"create", "shell", "--help"}, output); err != nil || output.Truncated() {
		return &Error{Kind: Unavailable, Message: "cannot inspect sbx CLI safety capabilities", cause: err}
	}
	for _, required := range []string{"--skills", "--deny-network", "--cpus", "--memory", "--template"} {
		if !strings.Contains(output.Text(), required) {
			return &Error{Kind: Unavailable, Message: "sbx CLI must support " + required + "; upgrade before enabling sandbox execution"}
		}
	}
	if err := r.invoke(ctx, []string{"ls", "--quiet"}, newOutputBuffer(4096)); err != nil {
		return &Error{Kind: Unavailable, Message: "sbx daemon is unavailable or not authenticated; run sbx login", cause: err}
	}
	return nil
}

// The host never interprets model argv. This fixed guest wrapper uses "$@",
// clears guest startup variables and executes as an unprivileged numeric UID.
// Guest rlimits are inherited by descendants, in addition to the VM's CPU/RAM
// limits and external deny-all network policy. Missing utilities fail closed.
const guestWrapper = `umask 077
mkdir -p /tmp/agentflow-work || exit 125
cd /tmp/agentflow-work || exit 125
exec /usr/bin/prlimit --nproc=64 --nofile=128 --core=0 --fsize=16777216 --as="$1" -- /usr/bin/timeout --signal=KILL "$2" /bin/sh -c 'shift 2; exec "$@"' command "$@"`

func (r *Runner) command(name string, args []string) []string {
	command := []string{"exec", "--user", "65534:65534", name, "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=/tmp/agentflow-work", "/usr/bin/setpriv", "--no-new-privs", "/bin/sh", "-c", guestWrapper, "command", fmt.Sprint(int64(r.options.MemoryMiB) * 1024 * 1024 / 2), fmt.Sprintf("%ds", max(1, int(r.options.Timeout.Seconds())))}
	return append(command, args...)
}

func (r *Runner) runCLI(ctx context.Context, args []string, output *outputBuffer) error {
	command := exec.CommandContext(ctx, r.options.Executable, args...)
	command.Dir = os.TempDir()
	// HOME authenticates the operator's sbx CLI to its daemon, never the guest.
	// In particular, do not forward SSH_AUTH_SOCK, proxy or provider variables.
	command.Env = []string{"HOME=" + os.Getenv("HOME"), "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin", "LANG=C", "LC_ALL=C", "TERM=dumb", "NO_COLOR=1"}
	command.Stdout, command.Stderr = output, output
	command.WaitDelay = 500 * time.Millisecond
	return command.Run()
}

func exitCode(err error) (int, bool) {
	var exited *exec.ExitError
	if errors.As(err, &exited) && exited.ExitCode() >= 0 {
		return exited.ExitCode(), true
	}
	return 0, false
}
