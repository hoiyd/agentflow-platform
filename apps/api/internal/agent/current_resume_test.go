package agent

import (
	"context"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

func TestCurrentToolDefinitionNeverAcceptsLegacyFields(t *testing.T) {
	catalog, err := tool.NewCatalog(tool.Binding{
		Descriptor: tool.Descriptor{Name: "reader", Description: "read", Parameters: tool.ObjectSchema(nil, nil)},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return nil, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := catalog.Installed("reader")
	frozen := snapshotTools(catalog, []string{"reader"})[0]
	if !toolDefinitionMatches(binding, frozen) {
		t.Fatal("current definition rejected")
	}
	for name, mutate := range map[string]func(*domain.RuntimeToolSnapshot){
		"security":    func(value *domain.RuntimeToolSnapshot) { value.Security = policy.Capability{} },
		"schema":      func(value *domain.RuntimeToolSnapshot) { value.SchemaVersion = "" },
		"revision":    func(value *domain.RuntimeToolSnapshot) { value.DefinitionRevision = "" },
		"side_effect": func(value *domain.RuntimeToolSnapshot) { value.SideEffect = string(tool.SideEffectExternal) },
	} {
		t.Run(name, func(t *testing.T) {
			value := frozen
			mutate(&value)
			if toolDefinitionMatches(binding, value) {
				t.Fatal("incomplete frozen definition accepted")
			}
		})
	}
}

func TestProgressGuardRejectsUnsupportedSnapshotBeforeCache(t *testing.T) {
	runtime := &Runtime{store: fixturestore.New(), toolProgressGuards: map[string]*progress.Guard{}}
	for _, snapshot := range []*domain.RuntimeSnapshot{nil, {SchemaVersion: domain.CurrentRuntimeSnapshotVersion - 1}} {
		if _, err := runtime.progressGuardForRun("run", snapshot); err == nil {
			t.Fatal("missing or historical snapshot must not disable the frozen guard")
		}
	}
}
