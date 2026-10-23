// Package runclient is a thin HTTP consumer, not an alternative Run Runtime.
package runclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"agentflow-platform/apps/api/internal/apicontract"
)

const maxJSONBytes = 32 << 20

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var sessionValue = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type Options struct {
	APIURL, WebURL, Workspace, SessionFile string
}

type Client struct {
	options Options
	http    *http.Client
	cookie  *http.Cookie
}

// Error contains only local diagnostics and safe protocol metadata, never the
// response body, URL credentials or transport errors containing cookie values.
type Error struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Status         int    `json:"http_status,omitempty"`
	RetryAfter     string `json:"retry_after,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	After          int64  `json:"after"`
	RequestSHA256  string `json:"request_sha256,omitempty"`
}

func (e *Error) Error() string            { return e.Code + ": " + e.Message }
func problem(code, message string) *Error { return &Error{Code: code, Message: message} }

func New(options Options) (*Client, error) {
	if !safeOrigin(options.APIURL) || options.WebURL != "" && !safeOrigin(options.WebURL) {
		return nil, problem("invalid_options", "API and Web URLs must be secret-free HTTPS origins (HTTP only on loopback)")
	}
	if options.Workspace == "" {
		return nil, problem("invalid_options", "an explicit Workspace ID is required")
	}
	// BIGINT IDs stay strings at this boundary; the API validates ownership/range.
	if len(options.Workspace) > 19 || strings.Trim(options.Workspace, "0123456789") != "" || strings.HasPrefix(options.Workspace, "0") {
		return nil, problem("invalid_options", "Workspace ID must be a positive decimal integer")
	}
	options.APIURL = strings.TrimSuffix(options.APIURL, "/")
	options.WebURL = strings.TrimSuffix(options.WebURL, "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	// Admission can wait before returning headers. The command's total deadline
	// bounds that wait; a shorter header timeout would create avoidable unknowns.
	c := &Client{options: options, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if options.SessionFile != "" {
		info, err := os.Lstat(options.SessionFile)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, problem("invalid_options", "session file must be a regular, owner-only file (chmod 600)")
		}
		file, err := os.Open(options.SessionFile)
		if err != nil {
			return nil, problem("invalid_options", "cannot open session file")
		}
		defer file.Close()
		actual, err := file.Stat()
		if err != nil || !os.SameFile(info, actual) || actual.Mode().Perm()&0077 != 0 {
			return nil, problem("invalid_options", "session file changed while opening")
		}
		data, err := io.ReadAll(io.LimitReader(file, 4097))
		if err != nil || len(data) > 4096 {
			return nil, problem("invalid_options", "session file exceeds 4 KiB or cannot be read")
		}
		name, value, ok := strings.Cut(strings.TrimSpace(string(data)), "=")
		if !ok || name != "agentflow_session" && name != "__Host-agentflow_session" || !sessionValue.MatchString(value) {
			return nil, problem("invalid_options", "session file must contain one AgentFlow session cookie, not an OIDC token")
		}
		if name == "__Host-agentflow_session" && !strings.HasPrefix(options.APIURL, "https://") {
			return nil, problem("invalid_options", "secure session cookies require HTTPS")
		}
		c.cookie = &http.Cookie{Name: name, Value: value}
	}
	return c, nil
}

func safeOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return u.Scheme == "https" || u.Scheme == "http" && (host == "localhost" || ip != nil && ip.IsLoopback())
}

func (c *Client) request(ctx context.Context, method, path string, body []byte, after int64) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.options.APIURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, problem("invalid_options", "invalid request")
	}
	r.Header.Set("X-Workspace-ID", c.options.Workspace)
	r.Header.Set("Accept", "application/json")
	if strings.HasSuffix(path, "/events") || path == "/api/chat" {
		r.Header.Set("Accept", "text/event-stream")
	}
	if method != http.MethodGet {
		r.Header.Set("Content-Type", "application/json")
		if c.options.WebURL != "" {
			r.Header.Set("Origin", c.options.WebURL)
		}
	}
	if c.cookie != nil {
		r.AddCookie(c.cookie)
	}
	if after > 0 {
		r.Header.Set("Last-Event-ID", formatSequence(after))
	}
	response, err := c.http.Do(r)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, problem("transport_error", "API connection failed; no mutation was retried")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		// Do not echo arbitrary server error text, even if a proxy reflects cookies.
		return nil, &Error{Code: "http_error", Message: http.StatusText(response.StatusCode), Status: response.StatusCode, RetryAfter: safeRetryAfter(response.Header.Get("Retry-After"))}
	}
	return response, nil
}

func safeRetryAfter(raw string) string {
	if seconds, err := strconv.ParseInt(raw, 10, 32); err == nil && seconds >= 0 {
		return strconv.FormatInt(seconds, 10)
	}
	if date, err := http.ParseTime(raw); err == nil {
		return date.UTC().Format(http.TimeFormat)
	}
	return ""
}

func decodeJSON(reader io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(reader, maxJSONBytes+1))
	if err != nil || len(data) > maxJSONBytes {
		return problem("protocol_error", "JSON response exceeds 32 MiB or was truncated")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(value) != nil {
		return problem("protocol_error", "invalid JSON response")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return problem("protocol_error", "trailing JSON response")
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, value any) error {
	response, err := c.request(ctx, http.MethodGet, path, nil, 0)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decodeJSON(response.Body, value)
}

func runPath(id, suffix string) (string, error) {
	if !identifier.MatchString(id) {
		return "", problem("invalid_options", "invalid Run ID")
	}
	return "/api/runs/" + id + suffix, nil
}

func (c *Client) Cancel(ctx context.Context, id string) (apicontract.Run, error) {
	var run apicontract.Run
	path, err := runPath(id, "/cancel")
	if err != nil {
		return run, err
	}
	r, err := c.request(ctx, http.MethodPost, path, []byte(`{}`), 0)
	if err != nil {
		return run, err
	}
	defer r.Body.Close()
	if err = decodeJSON(r.Body, &run); err == nil && (run.Id != id || run.WorkspaceId != c.options.Workspace) {
		err = problem("protocol_error", "cancel response identity mismatch")
	}
	return run, err
}

func exitCode(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return 124
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var detail *Error
	if errors.As(err, &detail) {
		switch {
		case detail.Code == "invalid_options":
			return 2
		case detail.Code == "submission_unknown":
			return 7
		case detail.Status == 401 || detail.Status == 403:
			return 4
		case detail.Status == 429:
			return 8
		}
	}
	return 1
}
