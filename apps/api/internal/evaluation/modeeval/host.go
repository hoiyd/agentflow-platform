package modeeval

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentflow-platform/apps/api/app"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/config"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/filesystem"
	"agentflow-platform/apps/api/internal/tool/policy"
)

type requestCeiling struct {
	ctx       context.Context
	mu        sync.Mutex
	max, used int
}

func (g *requestCeiling) AcquireRequest(ctx context.Context, _ string, _ int) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := g.ctx.Err(); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.used >= g.max {
		return nil, failure.New(failure.Definition{Message: "evaluation physical request limit reached", Info: failure.Info{Code: "evaluation_request_budget_exhausted", Source: "mode_evaluation", Category: failure.CategoryCapacity}})
	}
	g.used++
	return func() {}, nil
}
func (g *requestCeiling) count() int { g.mu.Lock(); defer g.mu.Unlock(); return g.used }

type host struct {
	application                   *app.Application
	api, embedding                *httptest.Server
	storage                       *store.PostgresStore
	workspace, agentID, directory string
	guard                         *requestCeiling
	drop                          func() error
}

func newHost(ctx context.Context, data Dataset, opts Options) (_ *host, err error) {
	h := &host{guard: &requestCeiling{ctx: ctx, max: opts.MaxRequests}}
	defer func() {
		if err != nil {
			_ = h.close()
		}
	}()
	database, drop, err := isolatedDatabase(ctx, opts.DatabaseURL)
	if err != nil {
		return nil, err
	}
	h.drop = drop
	h.directory, err = os.MkdirTemp("", "agentflow-mode-eval-")
	if err != nil {
		return nil, err
	}
	h.storage, err = store.NewPostgresStore(database)
	if err != nil {
		return nil, err
	}
	if err = h.storage.InitializeWorkspaceLifecycle(ctx); err != nil {
		return nil, err
	}
	h.workspace, err = h.storage.DefaultWorkspace(ctx, identity.SuperUserID)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(h.directory, "source")
	if err = os.Mkdir(root, 0700); err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(root, "brief.md"), []byte(data.Material), 0600); err != nil {
		return nil, err
	}
	write := func(name string, value any) error {
		b, e := json.Marshal(value)
		if e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(h.directory, name), b, 0600)
	}
	if err = write("roots.json", map[string]any{"roots": []filesystem.RootConfig{{ID: "source", WorkspaceID: h.workspace, Path: root}}}); err != nil {
		return nil, err
	}
	fs, err := filesystem.New(filepath.Join(h.directory, "roots.json"), h.storage)
	if err != nil {
		return nil, err
	}
	tools := tool.DefaultConfig()
	tools.EnabledTools = []string{"file_read"}
	for _, b := range fs.Bindings() {
		if b.Descriptor.Name == "file_read" {
			tools.SecurityPolicy.Rules = append(tools.SecurityPolicy.Rules, policy.Rule{ID: "evaluation-read", Tool: "file_read", Action: policy.ActionAllowAndLog, Capability: b.Descriptor.Security})
		}
	}
	if err = tool.SaveConfig(filepath.Join(h.directory, "tools.json"), tools); err != nil {
		return nil, err
	}
	zero := 0.0
	generation := domain.GenerationPolicy{AnswerStream: domain.GenerationProfile{Temperature: &zero}, Completion: domain.GenerationProfile{Temperature: &zero}}
	if err = write("routes.json", routing.RouteFile{Routes: []routing.RouteConfig{{
		ID: "evaluation", BaseURL: opts.BaseURL, Model: opts.Model,
		CredentialEnvironment: opts.CredentialEnvironment,
		RequestTimeoutSeconds: max(1, int(opts.TimeoutMS/1000)),
		Capabilities:          routing.Capabilities{Streaming: true, ToolCalling: true, StructuredOutput: true},
		GenerationPolicy:      &generation, ContextWindowTokens: 32768, MaxOutputTokens: 2048,
		Pricing: routing.Pricing{Source: "unknown"},
	}}}); err != nil {
		return nil, err
	}
	// Auxiliary stages use a fixed local embedding transport over an empty corpus.
	// They never send the synthetic task to a separate external embedding service.
	h.embedding = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0]}],"model":"empty-corpus-fixture"}`))
	}))
	cfg := config.Config{
		AuthMode: "local", DatabaseURL: database,
		AllowedOrigins: "http://localhost:3000", AuthSessionTTL: time.Hour,
		ModelRouteConfigPath: filepath.Join(h.directory, "routes.json"),
		ToolConfigPath:       filepath.Join(h.directory, "tools.json"),
		FilesystemRootsPath:  filepath.Join(h.directory, "roots.json"),
		EmbeddingBaseURL:     h.embedding.URL + "/v1", EmbeddingModel: "empty-corpus-fixture",
		EmbeddingDimensions: 2, EmbeddingRequestTimeout: time.Second,
		RouterMode: agent.RouterModeQuery, ModelRetryMaxAttempts: 1,
		MaxConcurrentRuns: 1, MaxConcurrentModelRequests: 4, MaxConcurrentOwnerModelRequests: 2,
		RunMaxModelCalls: opts.SampleCalls, RunMaxTotalTokens: opts.SampleTokens,
		RunMaxToolCalls: 8, RunMaxRuntime: time.Duration(opts.TimeoutMS) * time.Millisecond,
		AutonomousMaxIterations: 2, AutonomousMaxToolCalls: 8,
		AutonomousMaxRuntime:          time.Duration(opts.TimeoutMS) * time.Millisecond,
		AutonomousMaxOutputCharacters: 30000,
		ModelContextWindowTokens:      32768, ModelOutputReserveTokens: 2048,
		ContextSafetyMarginTokens: 512, ContextHistoryMaxTokens: 16000,
		ContextMemoryMaxTokens: 1000, ContextKnowledgeMaxTokens: 1000,
		ContextToolResultMaxTokens: 3000, ContextCompactionMode: "off",
		MemoryAdaptiveExtractionMode: "off", MemorySyncQueueSize: 1,
		MemorySyncJobTimeout: time.Second, MemoryProviderMaxAttempts: 1,
		ModelRequestCaptureMode: "metadata_only", ModelRequestCaptureMaxBytes: 4096,
		ModelRequestCaptureRetention: time.Hour,
		ToolSchemaMode:               "eager", ToolProgressGuardEnabled: true,
		ToolProgressWarnAfter: 2, ToolProgressBlockAfter: 3, ToolProgressHaltAfter: 3,
		RerankerMode: "heuristic",
	}
	h.application, err = app.New(cfg, app.WithModelRequestGuard(h.guard))
	if err != nil {
		return nil, err
	}
	h.api = httptest.NewServer(h.application)
	worker, err := h.storage.ForWorkspace(domain.NewWorkspaceScope(h.workspace)).CreateAgent(domain.Agent{
		Name: "Evidence report reader", Description: "Read release report evidence and return exact sourced facts",
		SystemPrompt: "Read the authorized material. Return the requested JSON report with exact facts and quotation. Never perform writes.",
		Tools:        []string{"file_read"}, Executor: domain.DefaultAgentExecutor,
		RoutingHints: domain.AgentRoutingHints{Capabilities: []string{"evidence_report"}, TaskExamples: []string{data.Task}},
	})
	if err != nil {
		return nil, err
	}
	h.agentID = worker.ID
	return h, nil
}

func (h *host) close() error {
	if h.api != nil {
		h.api.CloseClientConnections()
	}
	var drainErr error
	if h.application != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		drainErr = h.application.Close(ctx)
	}
	if h.api != nil {
		h.api.Close()
	}
	if h.embedding != nil {
		h.embedding.Close()
	}
	if drainErr != nil {
		return errors.New("evaluation runtime did not drain; isolated database retained")
	}
	var cleanup []error
	if h.storage != nil {
		cleanup = append(cleanup, h.storage.Close())
	}
	if h.directory != "" {
		cleanup = append(cleanup, os.RemoveAll(h.directory))
	}
	if h.drop != nil {
		cleanup = append(cleanup, h.drop())
	}
	return errors.Join(cleanup...)
}

func isolatedDatabase(ctx context.Context, raw string) (string, func() error, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || !strings.Contains(strings.ToLower(u.Path), "test") {
		return "", nil, errors.New("require a dedicated Postgres test database URL with CREATEDB privileges")
	}
	db, err := sql.Open("pgx", raw)
	if err != nil {
		return "", nil, errors.New("cannot open evaluation database server")
	}
	var random [12]byte
	if _, err = rand.Read(random[:]); err != nil {
		_ = db.Close()
		return "", nil, err
	}
	name := "agentflow_eval_" + hex.EncodeToString(random[:])
	if _, err = db.ExecContext(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		_ = db.Close()
		return "", nil, errors.New("cannot create isolated evaluation database")
	}
	u.Path = "/" + name
	return u.String(), func() error {
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, `DROP DATABASE "`+name+`" WITH (FORCE)`)
		return err
	}, nil
}
