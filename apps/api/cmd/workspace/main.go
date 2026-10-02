package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/store"
)

type ownerMappings map[string]string

func (m ownerMappings) String() string { return "<legacy-workspace>=<user-id>" }
func (m ownerMappings) Set(value string) error {
	key, owner, ok := strings.Cut(value, "=")
	key, owner = strings.TrimSpace(key), strings.TrimSpace(owner)
	if !ok || key == "" || owner == "" {
		return fmt.Errorf("owner must be <legacy-workspace>=<user-id>")
	}
	if _, exists := m[key]; exists {
		return fmt.Errorf("duplicate owner mapping for %s", key)
	}
	m[key] = owner
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(redaction.Writer{Writer: os.Stderr}, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	flags := flag.NewFlagSet("workspace migrate", flag.ContinueOnError)
	owners := ownerMappings{}
	flags.Var(owners, "owner", "Explicit legacy namespace owner (repeatable)")
	apply := flags.Bool("apply", false, "Apply the previewed mapping atomically; back up the database first")
	timeout := flags.Duration("timeout", 2*time.Minute, "Database operation deadline")
	if err := flags.Parse(args); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 {
		return fmt.Errorf("usage: workspace [--owner <legacy>=<user-id>] [--apply] [--timeout 2m]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	storage, err := store.OpenWorkspaceMigrationStore(ctx, config.Load().DatabaseURL)
	if err != nil {
		return err
	}
	defer storage.Close()
	plan, err := storage.PreviewWorkspaceMigration(ctx, owners)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(map[string]any{"apply": *apply, "legacy_workspaces": plan}); err != nil {
		return err
	}
	if !*apply {
		return nil
	}
	if err = storage.ApplyWorkspaceMigration(ctx, owners); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "Workspace migration completed; restart the API before resuming traffic.")
	return nil
}
