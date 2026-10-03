package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/egress"
)

type httpVerifier struct {
	client      *http.Client
	outputLimit int64
}

type HTTPConfig struct {
	Method         string `json:"method"`
	URL            string `json:"url"`
	ExpectedStatus int    `json:"expected_status"`
}

func newHTTPVerifier(allowedHosts []string, outputLimit int) httpVerifier {
	return httpVerifier{client: &http.Client{
		Transport: egress.NewTransport(allowedHosts), Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return egress.ErrDenied },
	}, outputLimit: int64(outputLimit)}
}

func (httpVerifier) Type() domain.VerifierType { return domain.VerifierHTTP }
func (httpVerifier) Version() string           { return "http-v1" }

func (httpVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	config, err := decodeConfig[HTTPConfig](spec)
	if err != nil {
		return err
	}
	config.Method = strings.ToUpper(strings.TrimSpace(config.Method))
	if config.Method == "" {
		config.Method = http.MethodGet
	}
	if config.Method != http.MethodGet && config.Method != http.MethodHead {
		return invalidContract("http verifier " + spec.ID + " only supports GET or HEAD")
	}
	config.URL = strings.TrimSpace(config.URL)
	parsed, err := url.ParseRequestURI(config.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return invalidContract("http verifier " + spec.ID + " requires an absolute http(s) URL")
	}
	if parsed.User != nil {
		return invalidContract("http verifier " + spec.ID + " must not embed credentials in the URL")
	}
	if config.ExpectedStatus == 0 {
		config.ExpectedStatus = http.StatusOK
	}
	if config.ExpectedStatus < 100 || config.ExpectedStatus > 599 {
		return invalidContract("http verifier " + spec.ID + " expected_status is invalid")
	}
	return freezeConfig(spec, config)
}

func (v httpVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, _ Subject) Result {
	config, err := decodeConfig[HTTPConfig](&spec)
	if err != nil {
		return blocked(BlockedConfigInvalid, "http config is missing")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || parsed.User != nil || (config.Method != http.MethodGet && config.Method != http.MethodHead) {
		return blocked(BlockedPolicyDenied, "http check requires GET/HEAD and a credential-free allowed origin")
	}
	request, err := http.NewRequestWithContext(ctx, config.Method, parsed.String(), nil)
	if err != nil {
		return blocked(BlockedExecutionFailed, "create http request: "+err.Error())
	}
	response, err := v.client.Do(request)
	if err != nil {
		if errors.Is(err, egress.ErrDenied) {
			return blocked(BlockedPolicyDenied, "http destination or redirect is not permitted")
		}
		if ctx.Err() != nil {
			return blockedForContext(ctx, "http request timed out or was canceled")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return blocked(BlockedTimedOut, "http request timed out")
		}
		return blocked(BlockedExecutionFailed, "http request failed")
	}
	defer response.Body.Close()
	output := newCappedBuffer(int(v.outputLimit))
	// Artifact retention and network consumption are separate limits. Never drain
	// an unlimited response just to calculate a full-content hash.
	const maxResponseBytes = 1 << 20
	if _, readErr := io.Copy(output, io.LimitReader(response.Body, maxResponseBytes+1)); readErr != nil {
		if ctx.Err() != nil {
			return blockedForContext(ctx, "http response timed out or was canceled")
		}
		if errors.Is(readErr, context.DeadlineExceeded) {
			return blocked(BlockedTimedOut, "http response timed out")
		}
		return blocked(BlockedExecutionFailed, "read http response failed")
	}
	result := Result{
		Status: domain.VerificationPassed, Summary: fmt.Sprintf("http status %d matched", response.StatusCode),
		Details: map[string]any{"actual_status": response.StatusCode, "expected_status": config.ExpectedStatus},
		Artifacts: []Artifact{{
			Kind: "http_response_body", MediaType: "text/plain; charset=utf-8",
			Content: output.String(), ContentHash: output.Hash(), ByteSize: output.Total(), Truncated: output.Truncated(),
		}},
	}
	if output.Total() > maxResponseBytes {
		result.Status, result.Summary = domain.VerificationBlocked, "http response exceeds 1 MiB limit; hash and byte count cover observed bytes only"
		result.Artifacts[0].Truncated = true
		return withBlockedReason(result, BlockedExecutionFailed)
	}
	if response.StatusCode != config.ExpectedStatus {
		result.Status = domain.VerificationFailed
		result.Summary = fmt.Sprintf("expected http status %d, got %d", config.ExpectedStatus, response.StatusCode)
	}
	return result
}
