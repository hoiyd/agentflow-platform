package tool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/credential"
	"agentflow-platform/apps/api/internal/egress"
	"agentflow-platform/apps/api/internal/tool/policy"
)

const fakeTavilyKey = "tvly-private-example123456"

type tavilyRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn tavilyRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestTavilyMissingCredentialFailsBeforeRequest(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "")
	client, err := NewTavilyClient(credential.FromEnvironment("TAVILY_API_KEY"))
	if client != nil || !tavilyErrorCode(err, ErrorCredentialScope) {
		t.Fatalf("missing credential: client=%v err=%v", client, err)
	}
}

func TestTavilySearchUsesOnlyFixedHTTPSHostAndHeader(t *testing.T) {
	client := tavilyTestClient(t)
	called := 0
	client.http.Transport = tavilyRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		called++
		if request.Method != http.MethodPost || request.URL.String() != "https://api.tavily.com/search" ||
			request.Header.Get("Authorization") != "Bearer "+fakeTavilyKey || request.URL.RawQuery != "" {
			t.Fatalf("unexpected Tavily request: method=%s url=%s authorization=%q", request.Method, request.URL, request.Header.Get("Authorization"))
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"results":[]}`))}, nil
	})
	response, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
	if err != nil || called != 1 || string(response) != `{"results":[]}` {
		t.Fatalf("authorized request: calls=%d response=%s err=%v", called, response, err)
	}
}

func TestTavilyRejectsWrongHostBeforeNetwork(t *testing.T) {
	client := tavilyTestClient(t)
	client.endpoint = "https://attacker.example/search"
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("wrong host reached network")
		return nil, nil
	})
	_, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
	if !tavilyErrorCode(err, ErrorSecurityScopeInvalid) {
		t.Fatalf("wrong-host denial: %v", err)
	}
}

func TestTavilyProductionTransportAndDialDenial(t *testing.T) {
	client := tavilyTestClient(t)
	request, _ := http.NewRequest("GET", "http://127.0.0.1:1/credentials", nil)
	if _, err := client.http.Transport.RoundTrip(request); !errors.Is(err, egress.ErrDenied) {
		t.Fatalf("production transport did not enforce destination: %v", err)
	}
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, egress.ErrDenied })
	if _, err := client.Search(t.Context(), json.RawMessage(`{"query":"test"}`)); !tavilyErrorCode(err, ErrorSecurityScopeInvalid) || strings.Contains(err.Error(), fakeTavilyKey) {
		t.Fatalf("unsafe egress failure contract: %v", err)
	}
}

func TestTavilyRejectsInvalidOrCredentialBearingPayloadBeforeNetwork(t *testing.T) {
	client := tavilyTestClient(t)
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached network")
		return nil, nil
	})
	for _, payload := range []json.RawMessage{
		nil,
		json.RawMessage(`{"query":`),
		json.RawMessage(`{"query":"` + fakeTavilyKey + `"}`),
		json.RawMessage(`{"query":"\u0074vly-private-example123456"}`),
		json.RawMessage(`{"query":"` + strings.Repeat("x", tavilyMaxRequestBytes) + `"}`),
	} {
		if _, err := client.Search(context.Background(), payload); !tavilyErrorCode(err, ErrorInvalidArgs) {
			t.Fatalf("invalid payload was accepted: %v", err)
		}
	}
	var absent *TavilyClient
	if _, err := absent.Search(context.Background(), json.RawMessage(`{}`)); !tavilyErrorCode(err, ErrorSecurityScopeInvalid) {
		t.Fatalf("nil client was accepted: %v", err)
	}
	t.Setenv("TAVILY_UNSET_TEST_KEY", "")
	client.credential = credential.FromEnvironment("TAVILY_UNSET_TEST_KEY")
	if _, err := client.Search(context.Background(), json.RawMessage(`{}`)); !tavilyErrorCode(err, ErrorCredentialScope) {
		t.Fatalf("lost credential was accepted: %v", err)
	}
}

func TestTavilyRejectsCrossHostRedirect(t *testing.T) {
	client := tavilyTestClient(t)
	called := 0
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		called++
		return &http.Response{
			StatusCode: http.StatusTemporaryRedirect,
			Header:     http.Header{"Location": []string{"https://attacker.example/collect"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})
	_, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
	if called != 1 || !tavilyErrorCode(err, ErrorSecurityScopeInvalid) {
		t.Fatalf("redirect crossed egress boundary: calls=%d err=%v", called, err)
	}
}

func TestTavilyNeverReturnsCredentialInErrorsOrResults(t *testing.T) {
	client := tavilyTestClient(t)
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport echoed " + fakeTavilyKey)
	})
	_, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
	if !tavilyErrorCode(err, ErrorExecutionFailed) || strings.Contains(err.Error(), fakeTavilyKey) {
		t.Fatalf("transport error leaked credential: %v", err)
	}
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fakeTavilyKey))}, nil
	})
	_, err = client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
	if !tavilyErrorCode(err, ErrorProviderUnavailable) || strings.Contains(err.Error(), fakeTavilyKey) {
		t.Fatalf("upstream error leaked credential: %v", err)
	}
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"content":"` + fakeTavilyKey + `"}`))}, nil
	})
	response, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
	if err != nil || strings.Contains(string(response), fakeTavilyKey) || !strings.Contains(string(response), "[REDACTED]") {
		t.Fatalf("upstream response leaked credential: response=%s err=%v", response, err)
	}
}

func TestTavilyMapsProviderFailuresWithoutReturningProviderBody(t *testing.T) {
	client := tavilyTestClient(t)
	for _, test := range []struct {
		status int
		code   ErrorCode
	}{
		{status: http.StatusTooManyRequests, code: ErrorProviderRateLimited},
		{status: http.StatusServiceUnavailable, code: ErrorProviderUnavailable},
	} {
		client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader("private provider body"))}, nil
		})
		_, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
		if !tavilyErrorCode(err, test.code) || strings.Contains(err.Error(), "private provider body") {
			t.Fatalf("status %d: %v", test.status, err)
		}
		if cause := errors.Unwrap(err); cause == nil || !strings.Contains(cause.Error(), http.StatusText(test.status)) || strings.Contains(cause.Error(), "private provider body") {
			t.Fatalf("status %d lost safe diagnostic: %v", test.status, cause)
		}
	}
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	_, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`))
	if !tavilyErrorCode(err, ErrorExecutionTimeout) {
		t.Fatalf("timeout: %v", err)
	}
}

func TestTavilyRejectsOversizedOrMalformedResponse(t *testing.T) {
	client := tavilyTestClient(t)
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "oversized", body: strings.Repeat("x", tavilyMaxResponseBytes+1)},
		{name: "malformed", body: `{"results":`},
	} {
		t.Run(test.name, func(t *testing.T) {
			client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})
			if _, err := client.Search(context.Background(), json.RawMessage(`{"query":"test"}`)); !tavilyErrorCode(err, ErrorResultEncoding) {
				t.Fatalf("invalid response was accepted: %v", err)
			}
		})
	}
}

func TestTavilyCredentialDoesNotReachToolTraceOrResult(t *testing.T) {
	client := tavilyTestClient(t)
	client.http.Transport = tavilyRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"content":"` + fakeTavilyKey + `"}`))}, nil
	})
	capability := policy.NormalizeCapability(policy.Capability{Scope: policy.Scope{
		Network:     policy.NetworkScope{Mode: policy.NetworkExternal, Targets: []string{"api.tavily.com"}},
		Credentials: []string{TavilyCredentialScope},
	}})
	catalog, err := NewCatalogWithPolicy(policyFor("web_search", policy.ActionAllow, capability), Binding{
		Descriptor: Descriptor{Name: "web_search", Parameters: ObjectSchema(map[string]any{
			"query": map[string]any{"type": "string"},
		}, []string{"query"}), Security: capability},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) { return client.Search(ctx, args) },
	})
	if err != nil {
		t.Fatal(err)
	}
	tracer := &recordingTracer{}
	result := NewExecutor(catalog, ExecutorOptions{Tracer: tracer}).Execute(context.Background(), ExecutionRequest{
		Tool: "web_search", Arguments: json.RawMessage(`{"query":"test"}`), CredentialScopes: []string{TavilyCredentialScope},
	})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	for name, value := range map[string]any{"result": result, "start": tracer.started, "finish": tracer.finished} {
		encoded, err := json.Marshal(value)
		if err != nil || strings.Contains(string(encoded), fakeTavilyKey) {
			t.Fatalf("%s leaked Tavily credential: value=%s err=%v", name, encoded, err)
		}
	}
	client.endpoint = "https://attacker.example/search"
	denied := NewExecutor(catalog, ExecutorOptions{Tracer: tracer}).Execute(context.Background(), ExecutionRequest{
		Tool: "web_search", Arguments: json.RawMessage(`{"query":"test"}`), CredentialScopes: []string{TavilyCredentialScope},
	})
	if denied.Error == nil || denied.Error.Code != ErrorSecurityScopeInvalid || strings.Contains(denied.Error.Message, fakeTavilyKey) {
		t.Fatalf("typed egress denial was lost: %#v", denied.Error)
	}
}

func tavilyTestClient(t *testing.T) *TavilyClient {
	t.Helper()
	t.Setenv("TAVILY_API_KEY", fakeTavilyKey)
	client, err := NewTavilyClient(credential.FromEnvironment("TAVILY_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func tavilyErrorCode(err error, code ErrorCode) bool {
	typed, ok := err.(*ExecutionError)
	return ok && typed.Code == code
}
