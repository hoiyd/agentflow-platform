package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	toolpkg "agentflow-platform/apps/api/internal/tools"
)

func TestToolHandlersListAndToggleTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := toolpkg.SaveConfig(path, toolpkg.DefaultConfig()); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	manager, err := toolpkg.NewManager(path)
	if err != nil {
		t.Fatalf("new tool manager: %v", err)
	}
	handler := &Handler{tools: manager}

	listRecorder := httptest.NewRecorder()
	handler.listTools(listRecorder, httptest.NewRequest(http.MethodGet, "/api/tools", nil))
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list tools status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listed []toolpkg.ToolInfo
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listed); err != nil || !toolEnabled(listed, "calculator") {
		t.Fatalf("expected enabled calculator: items=%#v err=%v", listed, err)
	}

	disableRecorder := httptest.NewRecorder()
	handler.setToolEnabled(disableRecorder, httptest.NewRequest(http.MethodPost, "/api/tools/calculator/disable", nil), false)
	if disableRecorder.Code != http.StatusOK {
		t.Fatalf("disable tool status=%d body=%s", disableRecorder.Code, disableRecorder.Body.String())
	}
	var disabled []toolpkg.ToolInfo
	if err := json.Unmarshal(disableRecorder.Body.Bytes(), &disabled); err != nil || toolEnabled(disabled, "calculator") {
		t.Fatalf("expected disabled calculator: items=%#v err=%v", disabled, err)
	}
}

func TestToolHandlerRejectsMissingAndUnknownTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := toolpkg.SaveConfig(path, toolpkg.DefaultConfig()); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	manager, err := toolpkg.NewManager(path)
	if err != nil {
		t.Fatalf("new tool manager: %v", err)
	}
	handler := &Handler{tools: manager}

	tests := []struct {
		name   string
		path   string
		status int
	}{
		{name: "missing", path: "/api/tools//enable", status: http.StatusBadRequest},
		{name: "unknown", path: "/api/tools/not-installed/enable", status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.setToolEnabled(recorder, httptest.NewRequest(http.MethodPost, test.path, nil), true)
			if recorder.Code != test.status {
				t.Fatalf("expected status %d, got %d body=%s", test.status, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestListToolsProjectsReloadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := toolpkg.SaveConfig(path, toolpkg.DefaultConfig()); err != nil {
		t.Fatalf("save tool config: %v", err)
	}
	manager, err := toolpkg.NewManager(path)
	if err != nil {
		t.Fatalf("new tool manager: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatalf("corrupt tool config: %v", err)
	}
	changedAt := time.Now().Add(time.Second)
	if err := os.Chtimes(path, changedAt, changedAt); err != nil {
		t.Fatalf("advance tool config timestamp: %v", err)
	}
	handler := &Handler{tools: manager}
	assertHandlerFailure(t, handler.listTools, httptest.NewRequest(http.MethodGet, "/api/tools", nil), http.StatusInternalServerError)
}

func toolEnabled(items []toolpkg.ToolInfo, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return item.Enabled
		}
	}
	return false
}
