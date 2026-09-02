package main

import (
	"flag"
	"fmt"
	"os"

	"agentflow-platform/apps/api/internal/eventcatalog"
)

func main() {
	output := flag.String("output", "../../docs/runtime/event-catalog.md", "catalog output path")
	flag.Parse()
	if err := os.WriteFile(*output, []byte(eventcatalog.CatalogMarkdown()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
