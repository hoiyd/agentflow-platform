package verification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type httpVerifier struct {
	client       *http.Client
	allowedHosts map[string]bool
	outputLimit  int64
}

type HTTPConfig struct {
	Method         string `json:"method"`
	URL            string `json:"url"`
	ExpectedStatus int    `json:"expected_status"`
}

func newHTTPVerifier(client *http.Client, allowedHosts []string, outputLimit int) httpVerifier {
	if client == nil {
		client = &http.Client{}
	}
	allowed := make(map[string]bool, len(allowedHosts))
	for _, host := range allowedHosts {
		if host = strings.ToLower(strings.TrimSpace(host)); host != "" {
			allowed[host] = true
		}
	}
	return httpVerifier{client: client, allowedHosts: allowed, outputLimit: int64(outputLimit)}
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
		return blocked("http config is missing")
	}
	parsed, err := url.Parse(config.URL)
	if err != nil || !v.hostAllowed(parsed) {
		return blocked("http host is not allowlisted: " + parsedHost(parsed))
	}
	request, err := http.NewRequestWithContext(ctx, config.Method, parsed.String(), nil)
	if err != nil {
		return blocked("create http request: " + err.Error())
	}
	client := *v.client
	configuredRedirect := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if !v.hostAllowed(request.URL) {
			return fmt.Errorf("redirect host is not allowlisted: %s", request.URL.Host)
		}
		if configuredRedirect != nil {
			return configuredRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return blocked("http request timed out or was canceled")
		}
		return blocked("http request failed: " + err.Error())
	}
	defer response.Body.Close()
	output := newCappedBuffer(int(v.outputLimit))
	if _, readErr := io.Copy(output, response.Body); readErr != nil {
		return blocked("read http response: " + readErr.Error())
	}
	result := Result{
		Status: domain.VerificationPassed, Summary: fmt.Sprintf("http status %d matched", response.StatusCode),
		Details: map[string]any{"actual_status": response.StatusCode, "expected_status": config.ExpectedStatus},
		Artifacts: []Artifact{{
			Kind: "http_response_body", MediaType: "text/plain; charset=utf-8",
			Content: output.String(), ContentHash: output.Hash(), ByteSize: output.Total(), Truncated: output.Truncated(),
		}},
	}
	if response.StatusCode != config.ExpectedStatus {
		result.Status = domain.VerificationFailed
		result.Summary = fmt.Sprintf("expected http status %d, got %d", config.ExpectedStatus, response.StatusCode)
	}
	return result
}

func (v httpVerifier) hostAllowed(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	hostname := strings.ToLower(parsed.Hostname())
	host := strings.ToLower(parsed.Host)
	if v.allowedHosts[hostname] || v.allowedHosts[host] {
		return true
	}
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func parsedHost(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	return parsed.Host
}
