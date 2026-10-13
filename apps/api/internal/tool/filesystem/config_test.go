package filesystem

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilesystemConfigurationFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "roots.json")
	storage := &fixtureStore{}
	for _, value := range []string{
		`{`, `{"roots":[],"typo":true}`, `{"roots":[]} {}`,
		`{"roots":[{"id":"project","workspace_id":"1","path":"relative"}]}`,
		`{"roots":[{"id":"project","workspace_id":"1","path":"/"}]}`,
		`{"roots":[{"id":"project","workspace_id":"1","path":` + quoted(filepath.Join(root, ".ssh")) + `}]}`,
		`{"roots":[{"id":"../bad","workspace_id":"1","path":` + quoted(root) + `}]}`,
		`{"roots":[{"id":"project","workspace_id":"not-an-id","path":` + quoted(root) + `}]}`,
		`{"roots":[{"id":"project","workspace_id":"1","path":` + quoted(root) + `},{"id":"project","workspace_id":"1","path":` + quoted(root) + `}]}`,
		`{"roots":[{"id":"project","workspace_id":"1","path":` + quoted(filepath.Join(root, "missing")) + `}]}`,
		strings.Repeat(" ", 65537),
	} {
		writeFixture(t, config, value)
		if _, err := New(config, storage); err == nil {
			t.Fatalf("accepted invalid config: %.120s", value)
		}
	}
	writeFixture(t, config, `{"roots":[]}`)
	disabled, err := New("", storage)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range disabled.Bindings() {
		if binding.UnavailableReason != "filesystem_unconfigured" {
			t.Fatal(binding)
		}
	}
	if _, err = New(config, nil); err == nil {
		t.Fatal("missing Run authorization store accepted")
	}
	if err = os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if _, err = New(config, storage); err == nil {
		t.Fatal("missing config accepted")
	}
}
