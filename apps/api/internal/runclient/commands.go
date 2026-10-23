package runclient

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"agentflow-platform/apps/api/internal/apicontract"
)

const usage = `Usage: agentflow <start|observe|cancel|result> [options]
  start   --request <chat-request.json> [--detach] [--output <new-evidence.json>]
  observe --run <id> [--after <sequence>] [--output <new-evidence.json>]
  cancel  --run <id>
  result  --run <id> [--output <new-evidence.json>]
Common: --api-url <origin> --workspace <id> --web-url <frontend-origin>
        --session-file <owner-only-cookie-file> --deadline <duration>
Credentials are read from AGENTFLOW_SESSION_FILE or --session-file, never argv values.
Ctrl-C and deadline stop observation only. Only cancel stops execution.
`

// Execute is the CLI entry point, shared with the functional production gate.
// All API objects come from the generated contract; no Store/Runtime is imported.
func Execute(parent context.Context, args []string, out, diagnostic io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(out, usage)
		return 0
	}
	command := args[0]
	if command != "start" && command != "observe" && command != "cancel" && command != "result" {
		return reportError(diagnostic, problem("invalid_options", "unknown command; use --help"))
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	// flag's errors include arbitrary argument values; print only safe diagnostics.
	flags.SetOutput(io.Discard)
	var options Options
	flags.StringVar(&options.APIURL, "api-url", environment("AGENTFLOW_API_URL", "http://localhost:8080"), "API origin")
	flags.StringVar(&options.WebURL, "web-url", os.Getenv("AGENTFLOW_WEB_URL"), "configured frontend origin")
	flags.StringVar(&options.Workspace, "workspace", os.Getenv("AGENTFLOW_WORKSPACE_ID"), "authorized Workspace ID")
	flags.StringVar(&options.SessionFile, "session-file", os.Getenv("AGENTFLOW_SESSION_FILE"), "session cookie file")
	deadline := flags.Duration("deadline", 5*time.Minute, "total client deadline")
	var requestFile, runID, output string
	var after int64
	var detach bool
	if command == "start" {
		flags.StringVar(&requestFile, "request", "", "ChatRequest JSON file")
		flags.BoolVar(&detach, "detach", false, "return after Run identity is acknowledged")
	}
	if command != "start" {
		flags.StringVar(&runID, "run", "", "Run ID")
	}
	if command == "observe" {
		flags.Int64Var(&after, "after", 0, "last fully consumed durable sequence")
	}
	if command != "cancel" {
		flags.StringVar(&output, "output", "", "new private evidence file")
	}
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(out, usage)
			return 0
		}
		return reportError(diagnostic, problem("invalid_options", "invalid flags; use --help"))
	}
	if flags.NArg() != 0 || *deadline <= 0 || after < 0 || command == "start" && requestFile == "" || command != "start" && !identifier.MatchString(runID) || detach && output != "" {
		return reportError(diagnostic, problem("invalid_options", "invalid command options; detached start cannot export final evidence"))
	}
	ctx, cancel := context.WithTimeout(parent, *deadline)
	defer cancel()
	c, err := New(options)
	if err != nil {
		return reportError(diagnostic, err)
	}
	defer c.http.CloseIdleConnections()
	if output != "" {
		if _, err := os.Lstat(output); !os.IsNotExist(err) {
			return reportError(diagnostic, problem("output_failed", "evidence destination already exists or cannot be checked"))
		}
	}
	receipt := Receipt{RunID: runID}
	if command == "start" {
		file, err := os.Open(requestFile)
		if err != nil {
			return reportError(diagnostic, problem("invalid_options", "cannot open request file"))
		}
		var request apicontract.ChatRequest
		decoder := json.NewDecoder(io.LimitReader(file, (2<<20)+1))
		decoder.DisallowUnknownFields()
		decoder.UseNumber()
		err = decoder.Decode(&request)
		var trailing any
		tail := decoder.Decode(&trailing)
		file.Close()
		if err != nil || tail != io.EOF || request.Message == "" || request.Mode != nil && !request.Mode.Valid() || request.WorkspaceId != nil && *request.WorkspaceId != options.Workspace {
			return reportError(diagnostic, problem("invalid_options", "invalid ChatRequest JSON or Workspace mismatch"))
		}
		receipt, err = c.Start(ctx, request)
		if err != nil {
			return reportRunError(diagnostic, err, receipt, 0)
		}
		if err = emit(out, map[string]any{"kind": "submitted", "receipt": receipt}); err != nil {
			return reportRunError(diagnostic, err, receipt, after)
		}
		if detach {
			return 0
		}
	}
	if command == "cancel" {
		run, err := c.Cancel(ctx, receipt.RunID)
		if err != nil {
			return reportRunError(diagnostic, err, receipt, after)
		}
		if err = emit(out, run); err != nil {
			return reportRunError(diagnostic, err, receipt, after)
		}
		return 0
	}
	if command == "start" || command == "observe" {
		after, err = c.Observe(ctx, receipt.RunID, after, out)
		if err != nil {
			return reportRunError(diagnostic, err, receipt, after)
		}
	}
	report, err := c.Result(ctx, receipt.RunID)
	if err != nil {
		return reportRunError(diagnostic, err, receipt, after)
	}
	report.RequestSHA256 = receipt.RequestSHA256
	if err = saveReport(output, report); err != nil {
		return reportRunError(diagnostic, err, receipt, after)
	}
	if command == "result" {
		err = emit(out, report)
	} else {
		err = emit(out, map[string]any{"kind": "result", "report": report})
	}
	if err != nil {
		return reportRunError(diagnostic, err, receipt, after)
	}
	return resultExit(report)
}

func environment(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func reportRunError(out io.Writer, err error, receipt Receipt, after int64) int {
	var detail *Error
	if errors.As(err, &detail) {
		copy := *detail
		detail = &copy
	} else if errors.Is(err, context.Canceled) {
		detail = problem("client_interrupted", "client stopped; Run was not canceled")
	} else if errors.Is(err, context.DeadlineExceeded) {
		detail = problem("client_deadline", "client deadline exceeded; Run was not canceled")
	} else {
		detail = problem("client_error", "client failed; inspect the Run before retrying")
	}
	if receipt.RunID != "" {
		detail.RunID = receipt.RunID
	}
	if receipt.ConversationID != "" {
		detail.ConversationID = receipt.ConversationID
	}
	if receipt.RequestSHA256 != "" {
		detail.RequestSHA256 = receipt.RequestSHA256
	}
	detail.After = after
	_ = json.NewEncoder(out).Encode(detail)
	return exitCode(err)
}
func reportError(out io.Writer, err error) int { return reportRunError(out, err, Receipt{}, 0) }
