package verification

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type commandVerifier struct {
	workspaceRoot string
	allowed       map[string]bool
	outputLimit   int
}

func newCommandVerifier(workspaceRoot string, allowedCommands []string, outputLimit int) commandVerifier {
	root := ""
	if configured := strings.TrimSpace(workspaceRoot); configured != "" {
		root, _ = filepath.Abs(configured)
	}
	allowed := make(map[string]bool, len(allowedCommands))
	for _, item := range allowedCommands {
		if name := strings.TrimSpace(item); name != "" {
			allowed[name] = true
		}
	}
	return commandVerifier{workspaceRoot: root, allowed: allowed, outputLimit: outputLimit}
}

func (commandVerifier) Type() domain.VerifierType { return domain.VerifierCommand }
func (commandVerifier) Version() string           { return "command-v1" }

func (v commandVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, _ Subject) Result {
	if spec.Command == nil || len(spec.Command.Args) == 0 {
		return blocked("command config is missing")
	}
	executable := strings.TrimSpace(spec.Command.Args[0])
	if !v.allowed[executable] {
		return blocked("command is not allowlisted: " + executable)
	}
	if v.workspaceRoot == "" {
		return blocked("verification workspace root is not configured")
	}
	workingDirectory := filepath.Join(v.workspaceRoot, filepath.Clean(spec.Command.WorkingDirectory))
	relative, err := filepath.Rel(v.workspaceRoot, workingDirectory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return blocked("working directory escapes verification workspace")
	}

	command := exec.CommandContext(ctx, executable, spec.Command.Args[1:]...)
	command.Dir = workingDirectory
	output := newCappedBuffer(v.outputLimit)
	command.Stdout = output
	command.Stderr = output
	err = command.Run()
	result := Result{Status: domain.VerificationPassed, Summary: "command completed successfully", Output: output.String(), OutputHash: output.Hash(), OutputBytes: output.Total(), Truncated: output.Truncated()}
	if err == nil {
		code := 0
		result.ExitCode = &code
		return result
	}
	if ctx.Err() != nil {
		result.Status = domain.VerificationBlocked
		result.Summary = "command timed out or was canceled"
		return result
	}
	result.Status = domain.VerificationFailed
	result.Summary = "command failed"
	if exitError, ok := err.(*exec.ExitError); ok {
		code := exitError.ExitCode()
		result.ExitCode = &code
		result.Summary = fmt.Sprintf("command exited with code %d", code)
		return result
	}
	result.Status = domain.VerificationBlocked
	result.Summary = "command could not start: " + err.Error()
	return result
}
