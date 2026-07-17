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

func (v httpVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, _ Subject) Result {
	if spec.HTTP == nil {
		return blocked("http config is missing")
	}
	parsed, err := url.Parse(spec.HTTP.URL)
	if err != nil || !v.hostAllowed(parsed) {
		return blocked("http host is not allowlisted: " + parsedHost(parsed))
	}
	request, err := http.NewRequestWithContext(ctx, spec.HTTP.Method, parsed.String(), nil)
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
	body, readErr := io.ReadAll(io.LimitReader(response.Body, v.outputLimit+1))
	if readErr != nil {
		return blocked("read http response: " + readErr.Error())
	}
	total := len(body)
	truncated := int64(total) > v.outputLimit
	if truncated {
		body = body[:v.outputLimit]
	}
	result := Result{
		Status: domain.VerificationPassed, Summary: fmt.Sprintf("http status %d matched", response.StatusCode),
		Output: string(body), OutputBytes: total, Truncated: truncated,
	}
	if response.StatusCode != spec.HTTP.ExpectedStatus {
		result.Status = domain.VerificationFailed
		result.Summary = fmt.Sprintf("expected http status %d, got %d", spec.HTTP.ExpectedStatus, response.StatusCode)
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
