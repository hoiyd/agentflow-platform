package eventcatalog

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/projection"
)

func TestRegistryCoversEveryDeclaredRunEventType(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	domainFile := filepath.Join(filepath.Dir(currentFile), "..", "domain", "execution.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), domainFile, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	ast.Inspect(parsed, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 || spec.Type == nil {
			return true
		}
		identifier, ok := spec.Type.(*ast.Ident)
		literal, literalOK := spec.Values[0].(*ast.BasicLit)
		if ok && literalOK && identifier.Name == "RunEventType" {
			declared[literal.Value[1:len(literal.Value)-1]] = true
		}
		return true
	})
	for _, definition := range Definitions() {
		if !declared[string(definition.Type)] {
			t.Errorf("registry contains undeclared event %s", definition.Type)
		}
		delete(declared, string(definition.Type))
	}
	for eventType := range declared {
		t.Errorf("declared event %s is missing from registry", eventType)
	}
}

func TestRegistryContractsAndProjectionCoverageAreComplete(t *testing.T) {
	for _, definition := range Definitions() {
		if definition.Producer == "" || definition.PayloadSchema == "" || len(definition.Consumers) == 0 {
			t.Errorf("event %s has incomplete producer/payload/consumer metadata: %#v", definition.Type, definition)
		}
		if !strings.HasPrefix(definition.PayloadSchema, "event.") {
			t.Errorf("event %s payload schema %q is not a Go event contract", definition.Type, definition.PayloadSchema)
		}
		if definition.Lifecycle.Role == "" {
			t.Errorf("event %s has no lifecycle role", definition.Type)
		}
		if definition.Lifecycle.Role == LifecycleTerminal && definition.Lifecycle.TerminalFor == "" {
			t.Errorf("terminal event %s has no pairing", definition.Type)
		}
		declaresRunProjection := contains(definition.Consumers, "run_projection")
		if declaresRunProjection != projection.ConsumesRunEvent(definition.Type) {
			t.Errorf("event %s run projection coverage drift: catalog=%t reducer=%t", definition.Type, declaresRunProjection, projection.ConsumesRunEvent(definition.Type))
		}
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func TestGeneratedCatalogIsCurrent(t *testing.T) {
	_, currentFile, _, _ := runtime.Caller(0)
	docPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "docs", "event-catalog.md")
	want, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != CatalogMarkdown() {
		t.Fatal("docs/event-catalog.md is stale; run go run ./cmd/generate-event-catalog")
	}
}
