package eventcatalog

import (
	"fmt"
	"strings"
)

func CatalogMarkdown() string {
	var output strings.Builder
	output.WriteString("# Run Event Catalog\n\n")
	output.WriteString("This file is generated from `apps/api/internal/eventcatalog`. Producers must use a registered event and typed payload contract; durable stores reject live-only and unregistered events.\n\n")
	output.WriteString("| Event | Durability | Schema | Scope | Producer | Payload schema | Lifecycle | Terminal for | Consumers |\n")
	output.WriteString("| --- | --- | ---: | --- | --- | --- | --- | --- | --- |\n")
	for _, item := range Definitions() {
		scope := "run"
		if item.Scope.Stage == ScopeRequired {
			scope += "+stage"
		}
		if item.Scope.Turn == ScopeRequired {
			scope += "+turn"
		}
		lifecycle := string(item.Lifecycle.Role)
		if item.Lifecycle.Family != "" {
			lifecycle = item.Lifecycle.Family + ":" + lifecycle
		}
		fmt.Fprintf(&output, "| `%s` | %s | %d | %s | %s | `%s` | %s | `%s` | %s |\n",
			item.Type, item.Durability, item.SchemaVersion, scope, item.Producer, item.PayloadSchema,
			lifecycle, item.Lifecycle.TerminalFor, strings.Join(item.Consumers, ", "))
	}
	return output.String()
}
