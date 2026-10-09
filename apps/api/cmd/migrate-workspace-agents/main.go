// migrate-workspace-agents assigns legacy global Agent profiles to explicitly
// selected Workspaces. Back up the database and stop the old API before --apply.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/store"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, out, stderr io.Writer) int {
	flags := flag.NewFlagSet("migrate-workspace-agents", flag.ContinueOnError)
	flags.SetOutput(stderr)
	workspace := flags.String("workspace", "", "primary target Workspace ID (keeps original Agent IDs)")
	copyWorkspace := flags.String("copy-workspace", "", "optional additional Workspace receiving independent copies")
	apply := flags.Bool("apply", false, "apply assignment; defaults to preview")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return 0
	} else if err != nil {
		return 2
	}
	if *workspace == "" || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "--workspace is required; no ownership is inferred; positional arguments are not supported")
		return 2
	}
	s, err := store.NewPostgresStore(config.Load().DatabaseURL)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer s.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items, err := s.AssignLegacyAgents(ctx, *workspace, *copyWorkspace, *apply)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err = json.NewEncoder(out).Encode(map[string]any{"applied": *apply, "agents": items}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
