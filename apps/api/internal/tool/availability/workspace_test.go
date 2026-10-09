package availability

import (
	"context"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

func TestWorkspaceAllowlistIsIndependentAndNewToolsDefaultDenied(t *testing.T) {
	storage := fixturestore.New()
	catalog := tool.DefaultCatalog()
	a, _, err := Resolve(storage, "a", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.ResolveReady("calculator"); !ok {
		t.Fatal("initial service grant missing")
	}
	if err := storage.SetWorkspaceToolEnabled("a", "calculator", false); err != nil {
		t.Fatal(err)
	}
	a, items, err := Resolve(storage, "a", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.ResolveReady("calculator"); ok {
		t.Fatal("revoked Tool visible")
	}
	for _, item := range items {
		if item.Name == "calculator" && (item.ExcludedReason != "workspace_disabled" || item.ConfigRevision != 2 || !item.ServiceEnabled || item.WorkspaceEnabled) {
			t.Fatalf("wrong explanation: %#v", item)
		}
	}
	b, _, err := Resolve(storage, "b", catalog)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := b.ResolveReady("calculator"); !ok {
		t.Fatal("other Workspace changed")
	}
	if _, ok := catalog.ResolveReady("calculator"); !ok {
		t.Fatal("service catalog mutated")
	}
	added, err := catalog.CloneWith(tool.Binding{Descriptor: tool.Descriptor{Name: "new_reader", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return "ok", nil }})
	if err != nil {
		t.Fatal(err)
	}
	a, _, err = Resolve(storage, "a", added)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.ResolveReady("new_reader"); ok {
		t.Fatal("new Tool inherited old permission")
	}
	if err := storage.SetWorkspaceToolEnabled("a", "new_reader", true); err != nil {
		t.Fatal(err)
	}
	a, _, err = Resolve(storage, "a", added)
	if err != nil {
		t.Fatal(err)
	}
	if err := RequireTool(a, "new_reader"); err != nil {
		t.Fatal(err)
	}
	if err := RequireTool(a, "unknown"); err == nil {
		t.Fatal("unknown granted")
	}
	if err := added.SetEnabled("new_reader", false); err != nil {
		t.Fatal(err)
	}
	a, items, err = Resolve(storage, "a", added)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.ResolveReady("new_reader"); ok {
		t.Fatal("owner overrode operator")
	}
	for _, item := range items {
		if item.Name == "new_reader" && item.ExcludedReason != "service_disabled" {
			t.Fatalf("operator reason lost: %#v", item)
		}
	}
}

func TestEmptyWorkspaceAllowlistDoesNotReinitialize(t *testing.T) {
	storage := fixturestore.New()
	if _, err := storage.EnsureWorkspaceToolConfig("empty", nil); err != nil {
		t.Fatal(err)
	}
	catalog, items, err := Resolve(storage, "empty", tool.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Definitions()) != 0 {
		t.Fatal("empty allowlist granted Tools")
	}
	for _, item := range items {
		if item.Enabled {
			t.Fatal("Tool enabled")
		}
	}
}
