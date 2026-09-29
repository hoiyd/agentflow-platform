package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agentflow-platform/apps/api/internal/skill/install/fallback"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, fallback.Run)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, args []string, out, stderr io.Writer, installer func(context.Context, fallback.Options) (fallback.Report, error)) int {
	if len(args) == 0 || args[0] != "install" {
		fmt.Fprintln(stderr, "usage: skill install --repo owner/repo --path package/path [--ref revision] [--dest existing/root] [--apply]")
		return 2
	}
	flags := flag.NewFlagSet("skill install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Restricted Go fallback installer. Prefer Vercel Skills CLI; see docs/tools/skill-installation.md.")
		fmt.Fprintln(stderr, "Run from the repository root: go -C apps/api run ./cmd/skill install ...")
		flags.PrintDefaults()
	}
	var opts fallback.Options
	flags.StringVar(&opts.Repo, "repo", "", "public GitHub owner/repo")
	flags.StringVar(&opts.Path, "path", "", "repository-relative Skill directory")
	flags.StringVar(&opts.Ref, "ref", "", "branch/tag/commit for preview or apply; omitted: latest default-branch commit")
	flags.StringVar(&opts.Destination, "dest", "", "existing download root; defaults to the repository root's .agents/skills")
	flags.DurationVar(&opts.Timeout, "timeout", 30*time.Second, "complete operation deadline, at most 2m")
	flags.BoolVar(&opts.Apply, "apply", false, "publish validated package without changing runtime trust")
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil || flags.NArg() != 0 {
		return 2
	}
	sharedDirectory, err := projectSkillDirectory()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if opts.Destination == "" {
		opts.Destination = sharedDirectory
	}
	report, err := installer(ctx, opts)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(stderr, "could not write report; if installed, inspect install-receipt.json in the package directory:", err)
		return 1
	}
	if opts.Apply {
		fmt.Fprintf(stderr, "Installed: %s\nReview content, set TRUSTED_SKILL_DIRS to its parent installation directory, and restart the API. No bindings or Tool permissions changed.\n", report.Directory)
	} else {
		fmt.Fprintf(stderr, "Preview only; destination unchanged. To install exactly this revision, re-run with --ref %s --apply.\n", report.Commit)
	}
	return 0
}
