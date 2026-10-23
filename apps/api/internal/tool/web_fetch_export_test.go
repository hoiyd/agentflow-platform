package tool

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// WebBindingsForTest changes only external sockets, not the real Bindings,
// provider protocol, redirect rules, Executor, Context or persistence. It is
// compiled exclusively for external-package integration tests of this package.
func WebBindingsForTest(t *testing.T, server *httptest.Server, resolve WebSourceResolver) []Binding {
	t.Helper()
	endpoint, _ := url.Parse(server.URL)
	transport := tavilyRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		clone := request.Clone(request.Context())
		clone.URL.Host = endpoint.Host
		response, err := server.Client().Transport.RoundTrip(clone)
		if response != nil {
			response.Request = request
		}
		return response, err
	})
	search := tavilyTestClient(t)
	search.http.Transport = transport
	return []Binding{WebSearchTool(search), webFetchTool(resolve, func(string) http.RoundTripper { return transport })}
}
