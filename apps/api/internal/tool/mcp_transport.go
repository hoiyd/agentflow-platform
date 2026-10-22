package tool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/egress"
	"agentflow-platform/apps/api/internal/redaction"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpEndpoint struct {
	config    MCPServerConfig
	key       credential.Value
	http      *http.Client
	transport http.RoundTripper
}

type mcpAuthenticationError struct{ status int }

func (e *mcpAuthenticationError) Error() string {
	return fmt.Sprintf("MCP authentication rejected (HTTP %d)", e.status)
}

func newMCPEndpoint(config MCPServerConfig) *mcpEndpoint {
	u, _ := url.Parse(config.URL)
	transport := egress.NewTransport([]string{u.Scheme + "://" + u.Host})
	endpoint := &mcpEndpoint{config: config, key: credential.FromEnvironment(config.BearerTokenEnv), transport: transport}
	endpoint.http = &http.Client{Transport: endpoint, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return egress.ErrDenied }}
	return endpoint
}

func (e *mcpEndpoint) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.String() != e.config.URL || (request.Method != http.MethodPost && request.Method != http.MethodDelete) {
		return nil, egress.ErrDenied
	}
	copy := request.Clone(request.Context())
	// The SDK terminates old-protocol sessions with a best-effort DELETE after
	// cancellation. Keep cleanup inside a short independent bound as well.
	var cancel context.CancelFunc
	if copy.Method == http.MethodDelete {
		var ctx context.Context
		ctx, cancel = context.WithTimeout(copy.Context(), 250*time.Millisecond)
		copy = copy.Clone(ctx)
	}
	copy.Header.Del("Authorization")
	if e.key.Available() {
		copy.Header.Set("Authorization", "Bearer "+e.key.Reveal())
	}
	response, err := e.transport.RoundTrip(copy)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, err
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		response.Body.Close()
		if cancel != nil {
			cancel()
		}
		return nil, &mcpAuthenticationError{status: response.StatusCode}
	}
	response.Body = &mcpBoundedBody{ReadCloser: response.Body, remaining: mcpMaxResponseBytes, cancel: cancel}
	return response, nil
}

type mcpBoundedBody struct {
	io.ReadCloser
	remaining int
	cancel    context.CancelFunc
}

// The SDK detaches connection and cancellation-notification contexts. These
// sessions belong to exactly one operation, including their auxiliary requests.
type mcpOperationTransport struct {
	endpoint *mcpEndpoint
	ctx      context.Context
}

func (t mcpOperationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	if t.ctx.Err() != nil {
		cancel()
	}
	response, err := t.endpoint.RoundTrip(request.Clone(ctx))
	if err != nil {
		stop()
		cancel()
		return nil, err
	}
	response.Body = &mcpBoundedBody{ReadCloser: response.Body, remaining: mcpMaxResponseBytes, cancel: func() { stop(); cancel() }}
	return response, nil
}

func (b *mcpBoundedBody) Close() error {
	if b.cancel != nil {
		defer b.cancel()
	}
	return b.ReadCloser.Close()
}

func (b *mcpBoundedBody) Read(p []byte) (int, error) {
	if len(p) > b.remaining+1 {
		p = p[:b.remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	if n > b.remaining {
		return 0, fmt.Errorf("MCP response exceeds %d bytes", mcpMaxResponseBytes)
	}
	b.remaining -= n
	return n, err
}

func (e *mcpEndpoint) connect(ctx context.Context) (*mcp.ClientSession, error) {
	if e.config.BearerTokenEnv != "" && !e.key.Available() {
		return nil, fmt.Errorf("MCP credential unavailable")
	}
	// No roots, sampling, elicitation, resource fetching or automatic multi-round
	// interactions. Per-operation sessions avoid hidden reconnect/retry authority.
	client := mcp.NewClient(&mcp.Implementation{Name: "agentflow", Version: "1"}, &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{}, MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	httpClient := *e.http
	httpClient.Transport = mcpOperationTransport{endpoint: e, ctx: ctx}
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: e.config.URL, HTTPClient: &httpClient, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: mcpMaxResponseBytes}, nil)
}

func (e *mcpEndpoint) safeText(value string) string {
	if key := e.key.Reveal(); key != "" {
		value = strings.ReplaceAll(value, key, "[REDACTED]")
	}
	value, _ = redaction.Text(value)
	if len(value) > 4096 {
		value = value[:4096] + " [truncated]"
	}
	return value
}

func (e *mcpEndpoint) callError(ctx context.Context, code ErrorCode, message string, err error) *ExecutionError {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) {
		return executionError(ErrorExecutionCanceled, "MCP call canceled", context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return executionError(ErrorExecutionTimeout, "MCP call timed out", context.DeadlineExceeded)
	}
	if err != nil {
		err = errors.New(e.safeText(err.Error()))
		log.Printf("MCP failure server=%s code=%s error=%s", e.config.ID, code, err)
	}
	return executionError(code, message, err)
}
