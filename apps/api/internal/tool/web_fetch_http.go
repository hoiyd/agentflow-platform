package tool

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/egress"
	"agentflow-platform/apps/api/internal/redaction"
	"agentflow-platform/apps/api/internal/tool/webcitation"
)

const webFetchMaxResponseBytes = 2 << 20

func fetchWebPage(ctx context.Context, source domain.WebCitation, transport func(string) http.RoundTripper) (webcitation.FetchResult, error) {
	var page webcitation.FetchResult
	normalized, ok := webcitation.NormalizeURL(source.URL)
	if !ok {
		return page, executionError(ErrorSecurityScopeInvalid, "Web fetch requires a public HTTPS source", nil)
	}
	u, _ := url.Parse(normalized)
	if redaction.ValidateValue(u.Query()) != nil {
		return page, executionError(ErrorSecurityScopeInvalid, "Web fetch refuses credential-shaped URL parameters", nil)
	}
	origin := u.Scheme + "://" + u.Host
	roundTripper := transport(origin)
	if closer, ok := roundTripper.(interface{ CloseIdleConnections() }); ok {
		defer closer.CloseIdleConnections()
	}
	client := &http.Client{
		Transport: roundTripper, Timeout: 10 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			normalized, valid := webcitation.NormalizeURL(request.URL.String())
			if !valid || len(via) >= 4 {
				return egress.ErrDenied
			}
			target, _ := url.Parse(normalized)
			if target.Scheme+"://"+target.Host != origin || redaction.ValidateValue(target.Query()) != nil {
				return egress.ErrDenied
			}
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, normalized, nil)
	if err != nil {
		return page, executionError(ErrorSecurityScopeInvalid, "Web fetch URL is invalid", nil)
	}
	request.Header.Set("Accept", "text/html, text/plain")
	// Explicit gzip disables net/http's implicit decompression so both the wire
	// bytes and the expanded bytes pass their own bounded read.
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("User-Agent", "AgentFlow-WebFetch/1.0")
	response, err := client.Do(request)
	if err != nil {
		return page, webFetchError(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := ErrorExecutionFailed
		if response.StatusCode == http.StatusTooManyRequests {
			code = ErrorProviderRateLimited
		} else if response.StatusCode >= 500 {
			code = ErrorProviderUnavailable
		}
		return page, executionError(code, fmt.Sprintf("Web fetch returned HTTP %d; page was not read", response.StatusCode), nil)
	}
	mediaType, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "text/html" && mediaType != "text/plain" {
		return page, executionError(ErrorResultEncoding, "Web fetch supports only HTML and plain text", nil)
	}
	charset := strings.ToLower(params["charset"])
	if charset != "" && charset != "utf-8" && charset != "utf8" && charset != "us-ascii" {
		return page, executionError(ErrorResultEncoding, "Web fetch supports only UTF-8 text", nil)
	}
	wire := &io.LimitedReader{R: response.Body, N: webFetchMaxResponseBytes + 1}
	var reader io.Reader = wire
	switch strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip":
		gz, err := gzip.NewReader(wire)
		if err != nil {
			return page, executionError(ErrorResultEncoding, "Web fetch gzip body is invalid", nil)
		}
		defer gz.Close()
		reader = gz
	default:
		return page, executionError(ErrorResultEncoding, "Web fetch content encoding is unsupported", nil)
	}
	body, err := io.ReadAll(io.LimitReader(reader, webFetchMaxResponseBytes+1))
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return page, webFetchError(err)
		}
		return page, executionError(ErrorResultEncoding, "Web fetch body could not be read", nil)
	}
	if len(body) > webFetchMaxResponseBytes || wire.N <= 0 || !utf8.Valid(body) {
		return page, executionError(ErrorResultEncoding, "Web fetch body is too large or not valid UTF-8", nil)
	}
	content, complete, err := extractWebText(body, mediaType)
	if err != nil {
		return page, err
	}
	finalURL, _ := webcitation.NormalizeURL(response.Request.URL.String())
	title, _ := limitSearchText(source.Title, webSearchMaxTitle)
	content, _ = redaction.Text(content)
	page = webcitation.FetchResult{
		SourceID: source.SourceID, Title: title, URL: normalized, FinalURL: finalURL,
		Content: content, ContentComplete: complete, Truncated: !complete,
		MediaType: mediaType, HTTPStatus: response.StatusCode, Status: "read", TrustBoundary: "untrusted_external_content",
	}
	if !complete {
		page.Status = "partial"
	}
	boundFetchResult(&page)
	return page, nil
}

func webFetchError(err error) *ExecutionError {
	switch {
	case errors.Is(err, egress.ErrDenied):
		return executionError(ErrorSecurityScopeInvalid, "Web fetch destination or redirect was blocked", nil)
	case errors.Is(err, context.Canceled):
		return executionError(ErrorExecutionCanceled, "Web fetch was canceled", context.Canceled)
	case errors.Is(err, context.DeadlineExceeded):
		return executionError(ErrorExecutionTimeout, "Web fetch timed out", context.DeadlineExceeded)
	default:
		return executionError(ErrorExecutionFailed, "Web fetch request failed; page was not read", nil)
	}
}
