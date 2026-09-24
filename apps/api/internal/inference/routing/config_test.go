package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRouteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.json")
	data := `{
  "routes": [
    {
      "id": "fast",
      "base_url": "https://models.example/v1",
      "model": "fast-chat",
      "credential_environment": "FAST_MODEL_API_KEY",
      "request_timeout_seconds": 300,
      "capabilities": {"tool_calling": true, "structured_output": true, "streaming": true},
      "context_window_tokens": 64000,
      "max_output_tokens": 4096,
      "priority": 120,
      "pricing": {"source": "provider_docs", "input_per_million_tokens_micros": 200000, "output_per_million_tokens_micros": 800000}
    }
  ]
}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadRouteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Routes) != 1 || config.Routes[0].ID != "fast" ||
		config.Routes[0].CredentialEnvironment != "FAST_MODEL_API_KEY" || config.Routes[0].RequestTimeoutSeconds != 300 {
		t.Fatalf("unexpected route config: %#v", config)
	}
	descriptor := config.Routes[0].Descriptor("openai_compatible")
	if descriptor.Provider != "openai_compatible" || descriptor.Model != "fast-chat" || descriptor.Priority != 120 {
		t.Fatalf("route config lost descriptor fields: %#v", descriptor)
	}
}

func TestRouteSamplingPolicyIsExplicitAndSecretFree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.json")
	data := `{"routes":[{"id":"fast","base_url":"https://models.example/v1","model":"fast-chat","credential_environment":"FAST_MODEL_API_KEY","request_timeout_seconds":300,"capabilities":{"streaming":true,"seed":true},"context_window_tokens":64000,"max_output_tokens":4096,"priority":100,"pricing":{"source":"test"},"generation_policy":{"answer_stream":{"temperature":0,"top_p":0.9,"seed":42},"completion":{"temperature":0.2,"top_p":1}}}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadRouteFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config.Routes[0].Descriptor("openai_compatible"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor map[string]any
	if err := json.Unmarshal(encoded, &descriptor); err != nil {
		t.Fatal(err)
	}
	policy, ok := descriptor["generation_policy"].(map[string]any)
	if !ok {
		t.Fatalf("route descriptor lost effective generation policy: %s", encoded)
	}
	answer, ok := policy["answer_stream"].(map[string]any)
	if !ok || answer["temperature"] != float64(0) || answer["top_p"] != 0.9 || answer["seed"] != float64(42) {
		t.Fatalf("answer policy changed: %#v", policy)
	}
}

func TestLoadRouteFileRejectsInvalidInput(t *testing.T) {
	if _, err := LoadRouteFile(""); err == nil {
		t.Fatal("empty route path was accepted")
	}
	for name, data := range map[string]string{
		"unknown field":   `{"routes":[],"secret":"no"}`,
		"trailing data":   `{"routes":[]} {}`,
		"empty catalog":   `{"routes":[]}`,
		"missing timeout": `{"routes":[{"id":"fast"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "routes.json")
			if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadRouteFile(path); err == nil {
				t.Fatal("invalid route config was accepted")
			}
		})
	}
	if _, err := LoadRouteFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("configured missing route file was ignored")
	}
}
