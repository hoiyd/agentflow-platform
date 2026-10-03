// Package sandbox runs structured commands in disposable, mountless Docker sbx
// microVMs. It never falls back to host command execution.
package sandbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

type Options struct {
	Executable      string
	StateDirectory  string
	Template        string
	AllowedCommands []string
	CPUs            int
	MemoryMiB       int
	MaxConcurrent   int
	Timeout         time.Duration
	MaxOutputBytes  int
}

type ErrorKind string

const (
	Denied        ErrorKind = "policy_denied"
	Unavailable   ErrorKind = "unavailable"
	Busy          ErrorKind = "capacity_exceeded"
	CleanupFailed ErrorKind = "cleanup_unconfirmed"
)

type Error struct {
	Kind    ErrorKind
	Message string
	cause   error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

type Result struct {
	SandboxID        string `json:"sandbox_id"`
	PolicyRevision   string `json:"policy_revision"`
	ExitCode         int    `json:"exit_code"`
	Output           string `json:"output"`
	ObservedBytes    int64  `json:"observed_bytes"`
	OutputHash       string `json:"output_hash"`
	Truncated        bool   `json:"truncated"`
	CleanupConfirmed bool   `json:"cleanup_confirmed"`
}

type Runner struct {
	options   Options
	lock      *os.File
	slots     chan struct{}
	closed    atomic.Bool
	unhealthy atomic.Bool
	lifecycle context.Context
	cancel    context.CancelFunc
	invoke    func(context.Context, []string, *outputBuffer) error
}

func New(options Options) (*Runner, error) {
	if !filepath.IsAbs(options.Executable) || strings.TrimSpace(options.StateDirectory) == "" || len(options.AllowedCommands) == 0 {
		return nil, errors.New("sandbox requires an absolute sbx executable, state directory and guest command allowlist")
	}
	options.AllowedCommands = slices.Clone(options.AllowedCommands)
	slices.Sort(options.AllowedCommands)
	for _, name := range options.AllowedCommands {
		if !strings.HasPrefix(name, "/") || filepath.Clean(name) != name || strings.ContainsAny(name, "\x00\n\r") {
			return nil, errors.New("sandbox command allowlist requires canonical absolute guest executables")
		}
	}
	if options.Template == "" {
		options.Template = "docker.io/docker/sandbox-templates:shell"
	}
	if strings.HasPrefix(options.Template, "-") || strings.ContainsAny(options.Template, "\x00\n\r ") {
		return nil, errors.New("sandbox template is invalid")
	}
	if options.CPUs == 0 {
		options.CPUs = 1
	}
	if options.MemoryMiB == 0 {
		options.MemoryMiB = 1024
	}
	if options.MaxConcurrent == 0 {
		options.MaxConcurrent = 1
	}
	if options.Timeout == 0 {
		options.Timeout = 2 * time.Minute
	}
	if options.MaxOutputBytes == 0 {
		options.MaxOutputBytes = 64 * 1024
	}
	if options.CPUs < 1 || options.CPUs > 4 || options.MemoryMiB < 512 || options.MemoryMiB > 4096 || options.MaxConcurrent < 1 || options.MaxConcurrent > 4 || options.Timeout < time.Second || options.Timeout > 5*time.Minute || options.MaxOutputBytes < 1 || options.MaxOutputBytes > 1024*1024 {
		return nil, errors.New("sandbox limits are outside the supported bounded profile")
	}
	if err := os.MkdirAll(options.StateDirectory, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(options.StateDirectory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("sandbox state directory must be a private non-symlink directory (0700)")
	}
	lock, err := lockDirectory(options.StateDirectory)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Runner{options: options, lock: lock, slots: make(chan struct{}, options.MaxConcurrent), lifecycle: ctx, cancel: cancel}
	r.invoke = r.runCLI
	return r, nil
}

// Revision freezes the execution profile, not host paths or credentials. Pin
// Template to an immutable image digest when image-level reproducibility matters.
func (r *Runner) Revision() string {
	profile := r.options
	profile.Executable, profile.StateDirectory = "", ""
	data, _ := json.Marshal(profile)
	hash := sha256.Sum256(data)
	return "sbx-v1-" + hex.EncodeToString(hash[:])
}

func (r *Runner) Timeout() time.Duration { return r.options.Timeout }

func (r *Runner) Run(ctx context.Context, args []string) (result Result, err error) {
	if r.closed.Load() {
		return result, &Error{Kind: Unavailable, Message: "sandbox runner is closed"}
	}
	if r.unhealthy.Load() {
		return result, &Error{Kind: CleanupFailed, Message: "sandbox cleanup is unconfirmed; recover before admitting more commands"}
	}
	if len(args) == 0 || len(args) > 64 || !slices.Contains(r.options.AllowedCommands, args[0]) {
		return result, &Error{Kind: Denied, Message: "sandbox executable is not allowlisted"}
	}
	size := 0
	for _, arg := range args {
		size += len(arg)
		if strings.ContainsRune(arg, 0) || size > 32768 {
			return result, &Error{Kind: Denied, Message: "sandbox arguments exceed the supported contract"}
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	select {
	case r.slots <- struct{}{}:
	default:
		return result, &Error{Kind: Busy, Message: "sandbox capacity is full"}
	}
	defer func() { <-r.slots }()
	executionCtx, cancel := context.WithTimeout(ctx, r.options.Timeout)
	stop := context.AfterFunc(r.lifecycle, cancel)
	defer cancel()
	defer stop()
	if r.closed.Load() {
		return result, &Error{Kind: Unavailable, Message: "sandbox runner is closed"}
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return result, err
	}
	name := "agentflow-" + hex.EncodeToString(id[:])
	result.SandboxID, result.PolicyRevision = name, r.Revision()
	// Persist ownership before asking sbx to create anything. A process crash at
	// any subsequent point leaves an exact name for startup recovery, not a scan
	// that could delete another installation's sandboxes.
	if err := r.record(name); err != nil {
		return result, &Error{Kind: Unavailable, Message: "sandbox ownership could not be persisted", cause: err}
	}
	defer func() {
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelCleanup()
		if cleanupErr := r.remove(cleanupCtx, name); cleanupErr != nil {
			r.unhealthy.Store(true)
			err = &Error{Kind: CleanupFailed, Message: "sandbox " + name + " cleanup could not be confirmed", cause: errors.Join(err, cleanupErr)}
		} else {
			result.CleanupConfirmed = true
		}
	}()
	create := []string{"create", "--name", name, "--cpus", fmt.Sprint(r.options.CPUs), "--memory", fmt.Sprintf("%dm", r.options.MemoryMiB), "--deny-network", "**", "--skills", "off", "--template", r.options.Template, "shell"}
	if err := r.invoke(executionCtx, create, newOutputBuffer(4096)); err != nil {
		return result, &Error{Kind: Unavailable, Message: "sbx could not create an isolated sandbox; check CLI support for --skills off, Docker login and daemon", cause: err}
	}
	output := newOutputBuffer(r.options.MaxOutputBytes)
	err = r.invoke(executionCtx, r.command(name, args), output)
	result.Output, result.ObservedBytes, result.OutputHash, result.Truncated = output.Text(), output.total, output.Hash(), output.Truncated()
	if executionCtx.Err() != nil {
		return result, executionCtx.Err()
	}
	if err != nil {
		if code, ok := exitCode(err); ok {
			result.ExitCode = code
			return result, nil
		}
		return result, &Error{Kind: Unavailable, Message: "sandbox command transport failed", cause: err}
	}
	return result, nil
}
