package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ownedName = regexp.MustCompile(`^agentflow-[0-9a-f]{32}$`)
var sandboxName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]+$`)

func (r *Runner) record(name string) error {
	file, err := os.OpenFile(filepath.Join(r.options.StateDirectory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.WriteString(name); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err == nil {
		err = r.syncDirectory()
	}
	return err
}

func (r *Runner) syncDirectory() error {
	dir, err := os.Open(r.options.StateDirectory)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (r *Runner) remove(ctx context.Context, name string) error {
	if !ownedName.MatchString(name) {
		return errors.New("invalid sandbox ownership record")
	}
	if err := r.invoke(ctx, []string{"rm", "--force", name}, newOutputBuffer(4096)); err != nil {
		// A crash during create may leave a durable name without a VM. Confirm
		// absence through the documented names-only inventory, never by parsing
		// an error string or assuming a failed delete means the VM is gone.
		output := newOutputBuffer(256 * 1024)
		if listErr := r.invoke(ctx, []string{"ls", "--quiet"}, output); listErr != nil || output.Truncated() {
			return err
		}
		for _, item := range strings.Fields(output.Text()) {
			if !sandboxName.MatchString(item) || item == name {
				return err
			}
		}
	}
	if err := os.Remove(filepath.Join(r.options.StateDirectory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return r.syncDirectory()
}

// Recover must run during startup, before this Runner is published. Its directory
// lock excludes other API processes; it removes only exact recorded names.
func (r *Runner) Recover(ctx context.Context) error {
	entries, err := os.ReadDir(r.options.StateDirectory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".lock" {
			continue
		}
		if !ownedName.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			return errors.New("unexpected sandbox ownership entry")
		}
		if err := r.remove(ctx, entry.Name()); err != nil {
			r.unhealthy.Store(true)
			return &Error{Kind: CleanupFailed, Message: "sandbox recovery could not confirm cleanup", cause: err}
		}
	}
	r.unhealthy.Store(false)
	return nil
}

func (r *Runner) Close(ctx context.Context) error {
	if r.closed.Swap(true) {
		return nil
	}
	r.cancel()
	// Wait for cleanup, not merely the caller's Tool timeout. On a shutdown
	// deadline keep the lock until process exit rather than permit two owners.
	for range cap(r.slots) {
		select {
		case r.slots <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.lock.Close()
}
