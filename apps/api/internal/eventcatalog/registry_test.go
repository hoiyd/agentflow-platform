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

	"agentflow-platform/apps/api/internal/domain"
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

func TestDefinitionAccessorsReturnDefensiveCopies(t *testing.T) {
	definition, ok := DefinitionFor(domain.EventRunCreated)
	if !ok || len(definition.Consumers) == 0 {
		t.Fatalf("missing run.created definition: %#v", definition)
	}
	definition.Consumers[0] = "mutated"
	again, _ := DefinitionFor(domain.EventRunCreated)
	if again.Consumers[0] == "mutated" {
		t.Fatal("DefinitionFor leaked the registry consumer slice")
	}
	if _, ok := DefinitionFor(domain.RunEventType("unknown.event")); ok {
		t.Fatal("unknown event should not have a definition")
	}

	definitions := Definitions()
	definitions[0].Consumers[0] = "mutated"
	fresh := Definitions()
	if fresh[0].Consumers[0] == "mutated" {
		t.Fatal("Definitions leaked the registry consumer slice")
	}
}

func TestValidateEnvelopeEnforcesRegisteredSchemaAndScope(t *testing.T) {
	valid := domain.RunEvent{
		Type: domain.EventStageStarted, SchemaVersion: domain.CurrentRunEventSchemaVersion,
		RunID: "run-1", StageID: "stage-1",
	}
	tests := []struct {
		name   string
		mutate func(*domain.RunEvent)
		want   string
	}{
		{name: "unregistered", mutate: func(item *domain.RunEvent) { item.Type = domain.RunEventType("unknown.event") }, want: "not registered"},
		{name: "missing run", mutate: func(item *domain.RunEvent) { item.RunID = " " }, want: "requires run_id"},
		{name: "unsupported schema", mutate: func(item *domain.RunEvent) { item.SchemaVersion++ }, want: "unsupported"},
		{name: "missing stage", mutate: func(item *domain.RunEvent) { item.StageID = "" }, want: "requires stage_id"},
		{name: "missing turn", mutate: func(item *domain.RunEvent) {
			item.Type = domain.EventTurnStarted
			item.StageID = ""
		}, want: "requires turn_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			item := valid
			test.mutate(&item)
			if err := ValidateEnvelope(item); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want text %q", err, test.want)
			}
		})
	}
	if err := ValidateEnvelope(valid); err != nil {
		t.Fatalf("valid event was rejected: %v", err)
	}
}

func TestValidateDurabilityAndLookup(t *testing.T) {
	durable := domain.RunEvent{
		Type: domain.EventRunCreated, SchemaVersion: domain.CurrentRunEventSchemaVersion, RunID: "run-1",
	}
	if err := ValidateDurableFact(durable); err != nil || !IsDurable(durable.Type) {
		t.Fatalf("durable event validation failed: durable=%t err=%v", IsDurable(durable.Type), err)
	}
	live := domain.RunEvent{
		Type: domain.EventModelDelta, SchemaVersion: domain.CurrentRunEventSchemaVersion,
		RunID: "run-1", Payload: map[string]any{"delta": "chunk"},
	}
	if err := ValidateDurableFact(live); err == nil || !strings.Contains(err.Error(), "live-only") {
		t.Fatalf("live event persistence error=%v", err)
	}
	if IsDurable(live.Type) || IsDurable(domain.RunEventType("unknown.event")) {
		t.Fatal("live and unknown event types must not be durable")
	}
	invalid := durable
	invalid.RunID = ""
	if err := ValidateDurableFact(invalid); err == nil {
		t.Fatal("invalid envelope should be rejected before durability lookup")
	}
}
