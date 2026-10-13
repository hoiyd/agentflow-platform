package tool

import "testing"

// Runtime prerequisites can disappear without changing the installed schema.
// Views must reflect that loss without mutating or recompiling the catalog.
func TestCatalogRechecksRuntimeAvailability(t *testing.T) {
	reason := ""
	binding := CalculatorTool()
	binding.CheckAvailability = func() string { return reason }
	catalog, err := NewCatalog(binding)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := catalog.Installed("calculator")
	reason = "filesystem_root_changed"
	unavailable, _ := catalog.Installed("calculator")
	if unavailable.UnavailableReason != reason || unavailable.Descriptor.DefinitionRevision != original.Descriptor.DefinitionRevision {
		t.Fatal(unavailable)
	}
	if _, ok := catalog.ResolveReady("calculator"); ok || len(catalog.Definitions()) != 0 || len(catalog.EnabledNames()) != 0 {
		t.Fatal("unavailable definition exposed")
	}
	if catalog.List()[0].UnavailableReason != reason {
		t.Fatal(catalog.List())
	}
	clone, err := catalog.CloneWith()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := clone.ResolveReady("calculator"); ok {
		t.Fatal("clone lost unavailability")
	}
	reason = ""
	if _, ok := catalog.ResolveReady("calculator"); !ok {
		t.Fatal("check mutated installed availability")
	}
	binding.CheckAvailability = func() string { panic("PRIVATE") }
	broken, err := NewCatalog(binding)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := broken.Installed("calculator")
	if value.UnavailableReason != "availability_check_failed" {
		t.Fatal(value)
	}
}
