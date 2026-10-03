package app

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/sandbox"
)

func newSandboxRunner(cfg config.Config) (*sandbox.Runner, error) {
	if !cfg.SandboxEnabled {
		return nil, nil
	}
	executable, err := exec.LookPath(cfg.SandboxExecutable)
	if err != nil {
		return nil, fmt.Errorf("locate sbx: %w", err)
	}
	runner, err := sandbox.New(sandbox.Options{
		Executable: executable, StateDirectory: cfg.SandboxStateDirectory,
		Template: cfg.SandboxTemplate, AllowedCommands: splitCSV(cfg.SandboxAllowedCommands),
		CPUs: cfg.SandboxCPUs, MemoryMiB: cfg.SandboxMemoryMiB,
		MaxConcurrent: cfg.SandboxMaxConcurrent, Timeout: cfg.SandboxTimeout,
		MaxOutputBytes: cfg.SandboxMaxOutputBytes,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := runner.Recover(ctx); err != nil {
		// No Runs have been admitted yet; always release the lock even when
		// readiness exhausted its deadline, so a repaired startup can retry.
		_ = runner.Close(context.Background())
		return nil, err
	}
	if err := runner.CheckAvailability(ctx); err != nil {
		_ = runner.Close(context.Background())
		return nil, err
	}
	return runner, nil
}
