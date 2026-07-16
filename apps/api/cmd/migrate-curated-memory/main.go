package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

func main() {
	cfg := config.Load()
	apply := flag.Bool("apply", false, "delete legacy message-derived memories; default is dry-run")
	storeDriver := flag.String("store-driver", cfg.StoreDriver, "file or postgres")
	dataPath := flag.String("data-path", cfg.DataPath, "file store JSON path")
	databaseURL := flag.String("database-url", cfg.DatabaseURL, "Postgres connection URL")
	flag.Parse()

	cleanupStore, closeStore, err := openStore(*storeDriver, *dataPath, *databaseURL)
	if err != nil {
		exitError(err)
	}
	defer closeStore()

	report, err := memory.CleanupLegacyMessageMemories(cleanupStore, *apply)
	if err != nil {
		exitError(fmt.Errorf("cleanup legacy message memories: %w", err))
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		exitError(fmt.Errorf("encode cleanup report: %w", err))
	}
}

func openStore(driver, dataPath, databaseURL string) (memory.LegacyCleanupStore, func(), error) {
	if strings.EqualFold(strings.TrimSpace(driver), "postgres") {
		if strings.TrimSpace(databaseURL) == "" {
			return nil, func() {}, fmt.Errorf("database-url or DATABASE_URL is required for postgres")
		}
		postgresStore, err := store.NewPostgresStore(databaseURL)
		if err != nil {
			return nil, func() {}, err
		}
		return postgresStore, func() { _ = postgresStore.Close() }, nil
	}
	fileStore, err := store.NewFileStore(dataPath)
	if err != nil {
		return nil, func() {}, err
	}
	return fileStore, func() {}, nil
}

func exitError(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
