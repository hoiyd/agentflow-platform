package filesystem

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

type fixtureStore struct {
	run    domain.Run
	denied bool
}

func (s *fixtureStore) GetRun(id string) (domain.Run, bool, error) { return s.run, id == s.run.ID, nil }
func (s *fixtureStore) CheckWorkspaceExecution(string) error {
	if s.denied {
		return errors.New("inactive")
	}
	return nil
}

type fixture struct {
	root, config string
	service      *Service
	store        *fixtureStore
	catalog      *tool.Catalog
	ctx          context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "roots.json")
	writeFixture(t, config, `{"roots":[{"id":"project","workspace_id":"1","path":`+quoted(root)+`}]}`)
	storage := &fixtureStore{run: domain.Run{ID: "run-files", ConversationID: "conversation-files", WorkspaceID: "1"}}
	service, err := New(config, storage)
	if err != nil {
		t.Fatal(err)
	}
	bindings := service.Bindings()
	rules := []policy.Rule{}
	for _, binding := range bindings {
		rules = append(rules, policy.Rule{ID: binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllow, Capability: binding.Descriptor.Security})
	}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "fixture-files-v1", DefaultAction: policy.ActionDeny, Rules: rules}, bindings...)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{root: root, config: config, service: service, store: storage, catalog: catalog, ctx: event.WithScope(t.Context(), event.Scope{RunID: storage.run.ID, ConversationID: storage.run.ConversationID})}
}
func quoted(s string) string { data, _ := json.Marshal(s); return string(data) }
func writeFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func (f fixture) call(ctx context.Context, name, args string) tool.ExecutionResult {
	return tool.NewExecutor(f.catalog, tool.ExecutorOptions{}).Execute(ctx, tool.ExecutionRequest{CallID: "file-call", RunID: f.store.run.ID, Tool: name, Arguments: json.RawMessage(args)})
}
func resultMap(t *testing.T, result tool.ExecutionResult) map[string]any {
	t.Helper()
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	data, err := json.Marshal(result.Result)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFilesystemListSearchReadAndRedactedPaging(t *testing.T) {
	f := newFixture(t)
	writeFixture(t, filepath.Join(f.root, "report.md"), "Technical evidence: 中文 report\nAuthorization: Bearer sk-abcdefghijklmnopqrstuvwxyz123456\n")
	list := resultMap(t, f.call(f.ctx, "file_list", `{"root":"project","path":"."}`))
	if len(list["entries"].([]any)) != 1 {
		t.Fatal(list)
	}
	search := resultMap(t, f.call(f.ctx, "file_search", `{"root":"project","path":".","query":"Technical"}`))
	if len(search["matches"].([]any)) != 1 {
		t.Fatal(search)
	}
	first := resultMap(t, f.call(f.ctx, "file_read", `{"root":"project","path":"report.md","limit":24}`))
	next := int(first["next_offset"].(float64))
	hash := first["content_hash"].(string)
	second := resultMap(t, f.call(f.ctx, "file_read", `{"root":"project","path":"report.md","offset":`+jsonNumber(next)+`,"expected_hash":`+quoted(hash)+`}`))
	if first["trust_boundary"] != "untrusted_filesystem_content" || second["redactions"].(float64) == 0 {
		t.Fatal(first, second)
	}
	if first["file_id"] == "" || first["root_revision"] == "" {
		t.Fatal(first)
	}
	writeFixture(t, filepath.Join(f.root, "report.md"), "Changed report")
	stale := f.call(f.ctx, "file_read", `{"root":"project","path":"report.md","offset":4,"expected_hash":`+quoted(hash)+`}`)
	if stale.Error == nil || stale.Error.Code != "filesystem_file_changed" {
		t.Fatal(stale)
	}
}
func jsonNumber(value int) string { data, _ := json.Marshal(value); return string(data) }

func TestFilesystemDeniedPathsAndScope(t *testing.T) {
	f := newFixture(t)
	writeFixture(t, filepath.Join(f.root, "safe.txt"), "safe")
	writeFixture(t, filepath.Join(f.root, ".env"), "PRIVATE")
	for _, name := range []string{".npmrc", ".netrc", ".git-credentials", ".kube/config", "service-account.json", ".data/backup.json", "docs/private/notes.md"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(f.root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(f.root, name), "PRIVATE")
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFixture(t, outside, "OUTSIDE")
	if err := os.Symlink(outside, filepath.Join(f.root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".env", filepath.Join(f.root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside.txt", outside, ".env", ".git/config", ".npmrc", ".netrc", ".git-credentials", ".kube/config", "service-account.json", ".data/backup.json", "docs/private/notes.md", "escape", "alias", "a/../safe.txt", "a\\safe.txt"} {
		t.Run(path, func(t *testing.T) {
			r := f.call(f.ctx, "file_read", `{"root":"project","path":`+quoted(path)+`}`)
			if r.Error == nil || r.Result != nil {
				t.Fatal(r)
			}
		})
	}
	for _, ctx := range []context.Context{t.Context(), event.WithScope(t.Context(), event.Scope{RunID: "missing", ConversationID: f.store.run.ConversationID}), event.WithScope(t.Context(), event.Scope{RunID: f.store.run.ID, ConversationID: "other"})} {
		if r := f.call(ctx, "file_read", `{"root":"project","path":"safe.txt"}`); r.Error == nil {
			t.Fatal(r)
		}
	}
	f.store.run.WorkspaceID = "2"
	if r := f.call(f.ctx, "file_read", `{"root":"project","path":"safe.txt"}`); r.Error == nil {
		t.Fatal(r)
	}
	f.store.run.WorkspaceID = "1"
	f.store.denied = true
	if r := f.call(f.ctx, "file_list", `{"root":"project"}`); r.Error == nil {
		t.Fatal(r)
	}
}

func TestFilesystemRevocationAndRootReplacement(t *testing.T) {
	f := newFixture(t)
	writeFixture(t, filepath.Join(f.root, "safe.txt"), "safe")
	original, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, f.config, `{"roots":[]}`)
	if r := f.call(f.ctx, "file_read", `{"root":"project","path":"safe.txt"}`); r.Error == nil || r.Error.Code != "filesystem_root_changed" {
		t.Fatal(r)
	}
	writeFixture(t, f.config, string(original))
	if err = os.Rename(f.root, f.root+"-old"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(f.root + "-old") })
	if err = os.Mkdir(f.root, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(f.root, "safe.txt"), "replacement")
	if r := f.call(f.ctx, "file_read", `{"root":"project","path":"safe.txt"}`); r.Error == nil || r.Error.Code != "filesystem_root_changed" {
		t.Fatal(r)
	}
	rebuilt, err := New(f.config, f.store)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := tool.NewCatalog(f.service.Bindings()...)
	after, _ := tool.NewCatalog(rebuilt.Bindings()...)
	a, _ := before.Installed("file_read")
	b, _ := after.Installed("file_read")
	if a.Descriptor.DefinitionRevision == b.Descriptor.DefinitionRevision {
		t.Fatal("replacement root did not change frozen definition")
	}
}

func TestFilesystemValidationBoundsAndCancellation(t *testing.T) {
	f := newFixture(t)
	writeFixture(t, filepath.Join(f.root, "binary"), "a\x00b")
	writeFixture(t, filepath.Join(f.root, "invalid"), string([]byte{0xff}))
	writeFixture(t, filepath.Join(f.root, "text"), "中文 text")
	for _, path := range []string{"binary", "invalid", "missing"} {
		if r := f.call(f.ctx, "file_read", `{"root":"project","path":`+quoted(path)+`}`); r.Error == nil {
			t.Fatal(r)
		}
	}
	for _, args := range []string{`{"root":"project","path":"text","offset":1,"expected_hash":"x"}`, `{"root":"project","path":"text","offset":4}`, `{"root":"project","path":"text","limit":0}`, `{"root":"project","path":"text","extra":true}`} {
		if r := f.call(f.ctx, "file_read", args); r.Error == nil {
			t.Fatal(r)
		}
	}
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	if r := f.call(ctx, "file_list", `{"root":"project"}`); r.Error == nil {
		t.Fatal(r)
	}
	denied, err := tool.NewCatalog(f.service.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	r := tool.NewExecutor(denied, tool.ExecutorOptions{}).Execute(f.ctx, tool.ExecutionRequest{Tool: "file_read", Arguments: json.RawMessage(`{"root":"project","path":"text"}`)})
	if r.Error == nil || r.Error.Code != tool.ErrorSecurityPolicyDenied {
		t.Fatal(r)
	}
}
