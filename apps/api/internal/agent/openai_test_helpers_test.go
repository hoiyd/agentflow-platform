package agent

import (
	"os"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/testsupport/modelroute"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

var fixtureModelServer = modelstream.NewServer()

func TestMain(m *testing.M) {
	code := m.Run()
	fixtureModelServer.Close()
	os.Exit(code)
}

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	client := newHTTPFixtureClientForTest()
	catalog, err := modelroute.Catalog(client, domain.ContextAssemblyConfig{}, domain.RuntimeRunBudget{})
	if err != nil {
		panic(err)
	}
	runtime := &Runtime{modelRoutes: catalog}
	modelRouting, err := runtime.captureModelRoutingSnapshot()
	if err != nil {
		panic(err)
	}
	identity := client.RuntimeIdentity()
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: ChatModeAutonomous,
		RunBudget:          &domain.RuntimeRunBudget{},
		Agent:              domain.RuntimeAgentSnapshot{ID: "agent_planner", Name: "Planner", SystemPrompt: "Plan carefully.", Executor: domain.DefaultAgentExecutor},
		Embedding:          domain.RuntimeEmbeddingSnapshot{Provider: identity.EmbeddingProvider, BaseURL: identity.EmbeddingBaseURL, Model: identity.EmbeddingModel, Dimensions: identity.EmbeddingDimensions},
		ModelRouting:       modelRouting,
		AutonomousLimits:   &domain.RuntimeLimitsSnapshot{MaxIterations: 5, MaxRuntimeMS: 300000, MaxOutputChars: 60000, MaxToolCalls: 20},
		ToolSecurityPolicy: policy.DefaultPolicy(),
		ToolProgressGuard:  progress.DefaultConfig(),
	}
}

func newHTTPFixtureClientForTest() *openai.Client {
	return openai.NewClientWithTimeoutAndEmbeddingModel("fixture-only", fixtureModelServer.URL, fixtureModelServer.URL, "fixture-model", "local_hash_embedding", 1536, time.Second)
}
