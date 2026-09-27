package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/redaction"
)

const (
	TavilyCredentialScope  = "tavily_search"
	tavilySearchURL        = "https://api.tavily.com/search"
	tavilyMaxRequestBytes  = 8192
	tavilyMaxResponseBytes = 1 << 20
)

var errTavilyRedirect = errors.New("Tavily redirect blocked")

// TavilyClient is the trusted egress boundary for the future web_search Binding.
// Neither its credential nor its transport belongs in Tool descriptors or snapshots.
type TavilyClient struct {
	credential credential.Value
	endpoint   string
	http       *http.Client
}

func NewTavilyClient(key credential.Value) (*TavilyClient, error) {
	if !key.Available() {
		return nil, executionError(ErrorCredentialScope, "Tavily credential is unavailable", nil)
	}
	return &TavilyClient{
		credential: key,
		endpoint:   tavilySearchURL,
		http: &http.Client{
			Timeout:   10 * time.Second,
			Transport: &http.Transport{TLSHandshakeTimeout: 10 * time.Second},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return errTavilyRedirect
			},
		},
	}, nil
}

// Search sends a bounded JSON request only to Tavily's official Search API.
// TOOL-023 owns argument validation and result normalization before model use.
func (c *TavilyClient) Search(ctx context.Context, payload json.RawMessage) (json.RawMessage, error) {
	if c == nil || c.endpoint != tavilySearchURL {
		return nil, executionError(ErrorSecurityScopeInvalid, "Tavily egress target is not allowed", nil)
	}
	key := c.credential.Reveal()
	if key == "" {
		return nil, executionError(ErrorCredentialScope, "Tavily credential is unavailable", nil)
	}
	if len(payload) == 0 || len(payload) > tavilyMaxRequestBytes {
		return nil, executionError(ErrorInvalidArgs, "Tavily request payload is invalid", nil)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return nil, executionError(ErrorInvalidArgs, "Tavily request payload is invalid", nil)
	}
	canonical, err := json.Marshal(fields)
	if err != nil || bytes.Contains(canonical, []byte(key)) {
		return nil, executionError(ErrorInvalidArgs, "Tavily request payload is invalid", nil)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, executionError(ErrorSecurityScopeInvalid, "Tavily request could not be created", nil)
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		if errors.Is(err, errTavilyRedirect) {
			return nil, executionError(ErrorSecurityScopeInvalid, "Tavily redirect blocked", nil)
		}
		return nil, executionError(ErrorExecutionFailed, "Tavily request failed", nil)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, executionError(ErrorExecutionFailed, "Tavily returned an unsuccessful status", nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, tavilyMaxResponseBytes+1))
	if err != nil || len(body) > tavilyMaxResponseBytes {
		return nil, executionError(ErrorResultEncoding, "Tavily response is unavailable or too large", nil)
	}
	redacted, _, err := redaction.JSON(body)
	if err != nil {
		return nil, executionError(ErrorResultEncoding, "Tavily response is not valid JSON", nil)
	}
	return bytes.ReplaceAll(redacted, []byte(key), []byte("[REDACTED]")), nil
}
