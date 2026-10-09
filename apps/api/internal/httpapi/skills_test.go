package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentpkg "agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/skill"
)

func TestSkillCatalogAndAgentBindingAPI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "api-method")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: api-method\ndescription: Inspect evidence\n---\nPRIVATE_METHOD_BODY"), 0600); err != nil {
		t.Fatal(err)
	}
	requiredDir := filepath.Join(filepath.Dir(dir), "required-method")
	if err := os.MkdirAll(requiredDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(requiredDir, "SKILL.md"), []byte("---\nname: required-method\ndescription: Calculate facts\nmetadata:\n  agentflow-required-tools: calculator\n---\nCalculate without granting authority."), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := skill.LoadDirectories([]string{dir, requiredDir})
	if err != nil {
		t.Fatal(err)
	}
	dependencies := completeHandlerDependencies(t)
	storage := fullStoreForTest(t, dependencies)
	owned, err := storage.CreateAgent(domain.Agent{ID: "owned-skill-agent", Name: "Owned Skill Agent"})
	if err != nil {
		t.Fatal(err)
	}
	dependencies.Skills = catalog
	dependencies.AgentRuntime = newRuntimeForTest(agentpkg.RuntimeOptions{Store: storage, Skills: catalog}, nil)
	handler, err := NewHandler(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	routes := handler.Routes()
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"name":"Denied dependency","tools":[],"skills":["required-method"]}`, 400},
		{`{"name":"Ready dependency","tools":["calculator"],"skills":["required-method"]}`, 201},
	} {
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/agents", bytes.NewBufferString(test.body)))
		if response.Code != test.status {
			t.Fatalf("dependency=%d %s", response.Code, response.Body.String())
		}
		if test.status == 400 && !strings.Contains(response.Body.String(), "skill_dependency_missing") {
			t.Fatal("missing dependency lost typed reason")
		}
	}
	response := httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/skills", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "api-method") || strings.Contains(response.Body.String(), "PRIVATE_METHOD_BODY") || strings.Contains(response.Body.String(), dir) {
		t.Fatalf("catalog=%s", response.Body.String())
	}
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/agents/"+owned.ID, bytes.NewBufferString(`{"skills":["api-method","api-method"]}`)))
	if response.Code != 200 {
		t.Fatalf("binding=%d %s", response.Code, response.Body.String())
	}
	var item domain.Agent
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil || len(item.Skills) != 1 {
		t.Fatalf("agent=%#v err=%v", item, err)
	}
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/agents/"+owned.ID, bytes.NewBufferString(`{"name":"Still bound"}`)))
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"skills":["api-method"]`) {
		t.Fatal("omitting Skills cleared bindings")
	}
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/agents/"+owned.ID, bytes.NewBufferString(`{"skills":["untrusted"]}`)))
	if response.Code != 400 || !strings.Contains(response.Body.String(), "skill_unavailable") {
		t.Fatalf("unknown accepted: %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	routes.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/agents/"+owned.ID, bytes.NewBufferString(`{"skills":[]}`)))
	if response.Code != 200 {
		t.Fatal("clear failed")
	}
	loaded, ok, err := storage.GetAgent(owned.ID)
	if err != nil || !ok || len(loaded.Skills) != 0 {
		t.Fatal("bindings not cleared")
	}
}
