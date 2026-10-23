package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
)

// User-supplied literal origins must not inherit operator-only local grants.
func TestPublicTransportDoesNotGrantPrivateOrigins(t *testing.T) {
	for _, address := range []string{"127.0.0.1:443", "10.0.0.1:443", "169.254.169.254:443", "[::1]:443"} {
		host, _, _ := net.SplitHostPort(address)
		transport := NewPublicTransport([]string{"https://" + net.JoinHostPort(host, "443")}).(*transport)
		if _, err := transport.dial(context.Background(), "tcp", address); !errors.Is(err, ErrDenied) {
			t.Fatalf("private origin %s: %v", address, err)
		}
	}
}

// Exercise the actual HTTP transport and DNS/Dial boundary. Only the physical
// test socket and fixture certificate identity change; production policy stays
// public-only and no insecure TLS setting is used.
func TestPublicHTTPTransportPinsDNSAndRejectsRebinding(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	tr := NewPublicTransport([]string{"https://example.com"}).(*transport)
	defer tr.CloseIdleConnections()
	tls := server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	tls.ServerName = endpoint.Hostname()
	tr.http.TLSClientConfig = tls
	var lookups, dials int
	tr.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		lookups++
		if lookups == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	tr.connect = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials++
		if address != "93.184.216.34:443" {
			t.Errorf("connection was not pinned to validated address: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, endpoint.Host)
	}
	client := &http.Client{Transport: tr}
	response, err := client.Get("https://example.com/body")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	tr.CloseIdleConnections()
	if _, err = client.Get("https://example.com/next"); !errors.Is(err, ErrDenied) {
		t.Fatalf("rebound DNS was not denied: %v", err)
	}
	if hits.Load() != 1 || lookups != 2 || dials != 1 {
		t.Fatalf("unsafe request reached HTTP: hits=%d lookups=%d dials=%d", hits.Load(), lookups, dials)
	}
}
