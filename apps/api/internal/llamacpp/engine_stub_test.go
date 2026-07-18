//go:build !llamacpp || !cgo

package llamacpp

import (
	"errors"
	"testing"
)

func TestStubReportsNativeBuildRequirement(t *testing.T) {
	if Available() {
		t.Fatal("Available() = true for stub build")
	}
	_, err := New(DefaultConfig("model.gguf"))
	if !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("New() error = %v", err)
	}
}
