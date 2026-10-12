//go:build linux || darwin

package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Isolated failure inventory: nonregular files must not block; links must never
// expose their target, even during replacement; scans/pages must have explicit
// bounds; archived/foreign Workspace grants must stop actual reads.
func TestFilesystemFailureMatrixPostgres(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	storage, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "roots.json")
	data, _ := json.Marshal(map[string]any{"roots": []RootConfig{{ID: "project", WorkspaceID: workspace, Path: root}}})
	writeFixture(t, config, string(data))
	service, err := New(config, storage)
	if err != nil {
		t.Fatal(err)
	}
	rules := []policy.Rule{}
	for _, binding := range service.Bindings() {
		rules = append(rules, policy.Rule{ID: binding.Descriptor.Name, Tool: binding.Descriptor.Name, Action: policy.ActionAllow, Capability: binding.Descriptor.Security})
	}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "file-fault-fixture", DefaultAction: policy.ActionDeny, Rules: rules}, service.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := storage.CreateConversationInWorkspace(workspace, "File boundary evidence")
	if err != nil {
		t.Fatal(err)
	}
	owned, err := storage.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "Reader", Tools: []string{"file_read"}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := storage.CreateRunWithContract(owned.ID, conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := event.WithScope(t.Context(), event.Scope{RunID: run.ID, ConversationID: conversation.ID})
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{Tracer: event.NewToolExecutionTracer(event.NewRecorder(storage), run.ID, "")})
	call := func(name, args string) tool.ExecutionResult {
		return executor.Execute(ctx, tool.ExecutionRequest{CallID: name, RunID: run.ID, Tool: name, Arguments: json.RawMessage(args)})
	}
	writeFixture(t, filepath.Join(root, "normal"), "中文 text\nsecond text\nthird text\n")
	t.Run("pages and empty files", func(t *testing.T) {
		writeFixture(t, filepath.Join(root, "empty"), "")
		if got := resultMap(t, call("file_read", `{"root":"project","path":"empty"}`)); got["content"] != "" || got["truncated"] != false {
			t.Fatal(got)
		}
		list := resultMap(t, call("file_list", `{}`))
		if len(list["roots"].([]any)) != 1 {
			t.Fatal(list)
		}
		a := resultMap(t, call("file_list", `{"root":"project","limit":1}`))
		if a["truncated"] != true {
			t.Fatal(a)
		}
		b := resultMap(t, call("file_list", `{"root":"project","offset":1,"limit":1}`))
		if len(b["entries"].([]any)) != 1 {
			t.Fatal(b)
		}
		results := resultMap(t, call("file_search", `{"root":"project","query":"text","limit":1}`))
		if results["truncated"] != true {
			t.Fatal(results)
		}
		results = resultMap(t, call("file_search", `{"root":"project","query":"text","offset":1,"limit":1}`))
		if results["matches"].([]any)[0].(map[string]any)["line"] != float64(2) {
			t.Fatal(results)
		}
		results = resultMap(t, call("file_search", `{"root":"project","query":"no match"}`))
		if len(results["matches"].([]any)) != 0 {
			t.Fatal(results)
		}
		for _, args := range []string{`{"root":"project","offset":999}`, `{"root":"project","path":"normal"}`} {
			if r := call("file_list", args); r.Error == nil {
				t.Fatal(r)
			}
		}
		if r := call("file_search", `{"root":"project","query":"text","offset":999}`); r.Error == nil {
			t.Fatal(r)
		}
	})
	t.Run("special files and content limits", func(t *testing.T) {
		if err = unix.Mkfifo(filepath.Join(root, "fifo"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Link(filepath.Join(root, "normal"), filepath.Join(root, "hardlink")); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(root, "binary"), "a\x00b")
		writeFixture(t, filepath.Join(root, "oversized"), strings.Repeat("x", maxFileBytes+1))
		for _, name := range []string{"fifo", "hardlink", "normal", "oversized", "."} {
			if r := call("file_read", `{"root":"project","path":`+quoted(name)+`}`); r.Error == nil {
				t.Fatal(r)
			}
		}
		if err = os.Remove(filepath.Join(root, "hardlink")); err != nil {
			t.Fatal(err)
		}
		r := resultMap(t, call("file_search", `{"root":"project","query":"text"}`))
		if r["skipped"].(float64) < 2 || r["scan_truncated"] != true {
			t.Fatal(r)
		}
		if err = os.Remove(filepath.Join(root, "oversized")); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("redaction expands page coordinates", func(t *testing.T) {
		writeFixture(t, filepath.Join(root, "expanded"), strings.Repeat("token=x\n", 130000))
		binding, _ := catalog.Installed("file_read")
		first, err := binding.Handler(ctx, json.RawMessage(`{"root":"project","path":"expanded","limit":4}`))
		if err != nil {
			t.Fatal(err)
		}
		page := first.(ReadResult)
		if page.TotalBytes <= maxFileBytes {
			t.Fatal("fixture did not expand after redaction")
		}
		args := `{"root":"project","path":"expanded","offset":` + jsonNumber(page.TotalBytes-4) + `,"expected_hash":` + quoted(page.ContentHash) + `}`
		validated, validationErr := catalog.ValidateCall("file_read", json.RawMessage(args))
		if validationErr != nil {
			t.Fatal(validationErr)
		}
		last, err := binding.Handler(ctx, validated.Arguments)
		if err != nil {
			t.Fatal(err)
		}
		if final := last.(ReadResult); final.Truncated || final.NextOffset != page.TotalBytes {
			t.Fatal(final)
		}
	})
	t.Run("search locates a late match for bounded reads", func(t *testing.T) {
		dir := filepath.Join(root, "locator")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(dir, "report"), strings.Repeat("prefix ", 1000)+"深度目标 evidence")
		found := resultMap(t, call("file_search", `{"root":"project","path":"locator","query":"深度目标"}`))
		match := found["matches"].([]any)[0].(map[string]any)
		offset, ok := match["offset"].(float64)
		if !ok || offset < 4096 || !strings.Contains(match["preview"].(string), "深度目标") {
			t.Fatal(match)
		}
		read := resultMap(t, call("file_read", `{"root":"project","path":"locator/report","offset":`+jsonNumber(int(offset))+`,"expected_hash":`+quoted(match["content_hash"].(string))+`}`))
		if !strings.HasPrefix(read["content"].(string), "深度目标") {
			t.Fatal(read)
		}
	})
	t.Run("bounded traversal", func(t *testing.T) {
		large := filepath.Join(root, "large")
		if err = os.Mkdir(large, 0700); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < maxEntries+1; i++ {
			writeFixture(t, filepath.Join(large, jsonNumber(i)), "text")
		}
		r := resultMap(t, call("file_list", `{"root":"project","path":"large","limit":50}`))
		if r["scan_truncated"] != true || len(r["entries"].([]any)) != 50 {
			t.Fatal(r)
		}
		if err = os.MkdirAll(filepath.Join(root, "nested/a/b/c"), 0700); err != nil {
			t.Fatal(err)
		}
		r = resultMap(t, call("file_list", `{"root":"project","path":"nested","depth":3}`))
		if r["scan_truncated"] != true {
			t.Fatal(r)
		}
		if r := call("file_read", `{"root":"project","path":"normal","offset":999,"expected_hash":`+quoted(strings.Repeat("a", 64))+`}`); r.Error == nil {
			t.Fatal(r)
		}
	})
	t.Run("replacement race", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside")
		writeFixture(t, outside, "OUTSIDE_CANARY")
		target := filepath.Join(root, "racing")
		writeFixture(t, target, "safe")
		done := make(chan struct{})
		var work sync.WaitGroup
		work.Add(1)
		go func() {
			defer work.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				_ = os.Remove(target)
				_ = os.Symlink(outside, target)
				_ = os.Remove(target)
				_ = os.WriteFile(target, []byte("safe"), 0600)
			}
		}()
		for range 40 {
			r := call("file_read", `{"root":"project","path":"racing"}`)
			encoded, _ := json.Marshal(r)
			if strings.Contains(string(encoded), "OUTSIDE_CANARY") {
				close(done)
				work.Wait()
				t.Fatal("escaping symlink was read")
			}
		}
		close(done)
		work.Wait()
	})
	t.Run("scan byte budget and match pages", func(t *testing.T) {
		// Exercise scan bounds through the real Handler without treating race
		// instrumentation cost as an Executor wall-time regression.
		binding, _ := catalog.Installed("file_search")
		search := func(args string) SearchResult {
			t.Helper()
			value, err := binding.Handler(ctx, json.RawMessage(args))
			if err != nil {
				t.Fatal(err)
			}
			return value.(SearchResult)
		}
		dir := filepath.Join(root, "bytes")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for index := 0; index < 4; index++ {
			writeFixture(t, filepath.Join(dir, jsonNumber(index)), strings.Repeat("中abc", 120000))
		}
		found := search(`{"root":"project","path":"bytes","query":"abc"}`)
		if !found.ScanTruncated || found.ScannedBytes > maxScanBytes || len(found.Matches) == 0 {
			t.Fatal(found)
		}
		dir = filepath.Join(root, "matches")
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(dir, "report"), strings.Repeat("hit\n", maxEntries+1))
		found = search(`{"root":"project","path":"matches","query":"hit","offset":490,"limit":20}`)
		if !found.ScanTruncated || len(found.Matches) != 10 {
			t.Fatal(found)
		}
	})
	t.Run("deadline and owner boundary", func(t *testing.T) {
		expired, cancel := context.WithDeadline(ctx, time.Now().Add(-time.Second))
		defer cancel()
		if r := executor.Execute(expired, tool.ExecutionRequest{Tool: "file_search", Arguments: json.RawMessage(`{"root":"project","query":"text"}`)}); r.Error == nil {
			t.Fatal(r)
		}
		foreignID := pgfixture.GrantMemberships(t, database, "https://fixture.invalid", "other-owner", "Unmapped")[0]
		c, err := storage.CreateConversationInWorkspace(foreignID, "Unmapped files")
		if err != nil {
			t.Fatal(err)
		}
		a, err := storage.ForWorkspace(domain.NewWorkspaceScope(foreignID)).CreateAgent(domain.Agent{Name: "Foreign", Tools: []string{"file_read"}})
		if err != nil {
			t.Fatal(err)
		}
		other, err := storage.CreateRunWithContract(a.ID, c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		foreignCtx := event.WithScope(t.Context(), event.Scope{RunID: other.ID, ConversationID: c.ID})
		if r := executor.Execute(foreignCtx, tool.ExecutionRequest{Tool: "file_read", Arguments: json.RawMessage(`{"root":"project","path":"normal"}`)}); r.Error == nil {
			t.Fatal(r)
		}
		if _, err = storage.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
			t.Fatal(err)
		}
		status := "archived"
		replacement, err := storage.CreateWorkspace(t.Context(), identity.SuperUserID, "Replacement", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = storage.UpdateWorkspace(t.Context(), identity.SuperUserID, workspace, domain.WorkspaceUpdate{Status: &status, ReplacementWorkspaceID: replacement.ID}, false); err != nil {
			t.Fatal(err)
		}
		if r := call("file_read", `{"root":"project","path":"normal"}`); r.Error == nil {
			t.Fatal(r)
		}
	})
}
