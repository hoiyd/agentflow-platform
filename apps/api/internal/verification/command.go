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

type CommandConfig struct {
	Args             []string `json:"args"`
	WorkingDirectory string   `json:"working_directory,omitempty"`
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

func (commandVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	config, err := decodeConfig[CommandConfig](spec)
	if err != nil {
		return err
	}
	if len(config.Args) == 0 || strings.TrimSpace(config.Args[0]) == "" {
		return invalidContract("command verifier " + spec.ID + " requires args")
	}
	config.Args = append([]string(nil), config.Args...)
	config.Args[0] = strings.TrimSpace(config.Args[0])
	config.WorkingDirectory = strings.TrimSpace(config.WorkingDirectory)
	if filepath.IsAbs(config.WorkingDirectory) {
		return invalidContract("command verifier " + spec.ID + " working_directory must be relative")
	}
	return freezeConfig(spec, config)
}

func (v commandVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, _ Subject) Result {
	config, err := decodeConfig[CommandConfig](&spec)
	if err != nil || len(config.Args) == 0 {
		return blocked("command config is missing")
	}
	executable := strings.TrimSpace(config.Args[0])
	if !v.allowed[executable] {
		return blocked("command is not allowlisted: " + executable)
	}
	if v.workspaceRoot == "" {
		return blocked("verification workspace root is not configured")
	}
	workingDirectory := filepath.Join(v.workspaceRoot, filepath.Clean(config.WorkingDirectory))
	relative, err := filepath.Rel(v.workspaceRoot, workingDirectory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return blocked("working directory escapes verification workspace")
	}

	command := exec.CommandContext(ctx, executable, config.Args[1:]...)
	command.Dir = workingDirectory
	output := newCappedBuffer(v.outputLimit)
	command.Stdout = output
	command.Stderr = output
	err = command.Run()
	result := Result{
		Status: domain.VerificationPassed, Summary: "command completed successfully",
		Artifacts: []Artifact{{
			Kind: "command_output", MediaType: "text/plain; charset=utf-8",
			Content: output.String(), ContentHash: output.Hash(), ByteSize: output.Total(), Truncated: output.Truncated(),
		}},
	}
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
