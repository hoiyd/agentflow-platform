package config

import (
	"testing"
	"time"
)

func TestRerankerConfigurationIsExplicitAndSecretFree(t *testing.T) {
	t.Chdir(t.TempDir())
	for key, value := range map[string]string{"RERANKER_MODE": "tei", "RERANKER_BASE_URL": "http://localhost:18082", "RERANKER_MODEL": "fixture-model", "RERANKER_REVISION": "fixture-revision", "RERANKER_TIMEOUT": "4s", "RERANKER_MAX_CONCURRENT_REQUESTS": "3"} {
		t.Setenv(key, value)
	}
	cfg := Load()
	if cfg.RerankerMode != "tei" || cfg.RerankerBaseURL != "http://localhost:18082" || cfg.RerankerModel != "fixture-model" || cfg.RerankerRevision != "fixture-revision" || cfg.RerankerTimeout != 4*time.Second || cfg.RerankerMaxConcurrentRequests != 3 {
		t.Fatal("reranker settings were not loaded")
	}
}
