package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemOutputConfigurationIsExclusive(t *testing.T) {
	root, config := t.TempDir(), filepath.Join(t.TempDir(), "roots.json")
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{
		`{"id":"output","workspace_id":"1","path":` + quoted(root) + `,"write_access":"shared"}`,
		`{"id":"output","workspace_id":"1","path":` + quoted(root) + `,"write_access":"managed_output"},{"id":"another","workspace_id":"2","path":` + quoted(root) + `}`,
	} {
		writeFixture(t, config, `{"roots":[`+extra+`]}`)
		if _, err := New(config, &fixtureStore{}); err == nil {
			t.Fatal("invalid or cross-owner managed root accepted", extra)
		}
	}
	inside := filepath.Join(root, "root-map.json")
	writeFixture(t, inside, `{"roots":[{"id":"output","workspace_id":"1","path":`+quoted(root)+`,"write_access":"managed_output"}]}`)
	if _, err := New(inside, &fixtureStore{}); err == nil {
		t.Fatal("root map placed inside model-writable output accepted")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(root), alias); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "output")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	readAlias := filepath.Join(alias, filepath.Base(root))
	writeFixture(t, config, `{"roots":[{"id":"output","workspace_id":"1","path":`+quoted(child)+`,"write_access":"managed_output"},{"id":"reader","workspace_id":"2","path":`+quoted(readAlias)+`}]}`)
	if _, err := New(config, &fixtureStore{}); err == nil {
		t.Fatal("cross-owner overlap through an ancestor alias accepted")
	}
	writeFixture(t, config, `{"roots":[{"id":"output","workspace_id":"1","path":`+quoted(root)+`,"write_access":"managed_output"}]}`)
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config, &fixtureStore{}); err == nil {
		t.Fatal("non-private output accepted")
	}
}
