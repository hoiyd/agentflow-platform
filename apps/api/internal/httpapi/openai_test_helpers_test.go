package httpapi

import (
	"time"

	"agentflow-platform/apps/api/internal/openai"
)

func newLocalFallbackOpenAIClientForTest() *openai.Client {
	return openai.NewClientWithTimeoutAndEmbeddingModel("", "", "https://embedding.test/v1", "test", "local-test-embedding", 1536, time.Second)
}
