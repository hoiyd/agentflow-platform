package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func TestOriginAuthorization(t *testing.T) {
	for _, tc := range []struct {
		allowed, target string
		pass            bool
	}{
		{"example.com", "https://example.com/path", true},
		{"example.com", "http://example.com/path", false},
		{"example.com", "https://example.com:8443/path", false},
		{"example.com:8443", "https://example.com:8443/path", false},
		{"example.com", "https://sub.example.com", false},
		{"example.com", "https://example.com.attacker.invalid", false},
		{"https://example.com:8443", "https://example.com:8443/path", true},
		{"", "http://localhost:8080", false},
		{"http://localhost:8080", "http://localhost:8080/health", true},
		{"http://LOCALHOST:8080", "http://localhost:8080/health", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8081", false},
		{"http://[::1]:8080", "http://[::1]:8080", true},
		{"127.0.0.1", "https://127.0.0.1", false},
		{"https://example.com/path", "https://example.com/path", false},
		{"https://example.com", "https://user:password@example.com", false},
		{"https://example.com", "file://example.com/path", false},
		{"*.example.com", "https://sub.example.com", false},
		{"https://example.com:0", "https://example.com:0", false},
	} {
		t.Run(tc.allowed+"->"+tc.target, func(t *testing.T) {
			transport := NewTransport([]string{tc.allowed}).(*transport)
			u, _ := url.Parse(tc.target)
			_, ok := transport.origins[origin(u)]
			if ok != tc.pass {
				t.Fatalf("origin authorization = %v, want %v", ok, tc.pass)
			}
		})
	}
}

func TestDialValidatesAllAddressesAndPinsConnection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		addresses []string
		allowed   string
		pass      bool
	}{
		{"public", []string{"93.184.216.34"}, "example.com", true},
		{"public IPv6", []string{"2606:4700::1111"}, "example.com", true},
		{"mixed DNS", []string{"93.184.216.34", "127.0.0.1"}, "example.com", false},
		{"private IPv6", []string{"fd00::1"}, "example.com", false},
		{"mapped loopback", []string{"::ffff:127.0.0.1"}, "example.com", false},
		{"metadata", []string{"169.254.169.254"}, "example.com", false},
		{"shared carrier", []string{"100.64.0.1"}, "example.com", false},
		{"benchmark network", []string{"198.18.0.1"}, "example.com", false},
		{"documentation IPv6", []string{"2001:db8::1"}, "example.com", false},
		{"additional documentation IPv6", []string{"3fff::1"}, "example.com", false},
		{"deprecated relay", []string{"192.88.99.1"}, "example.com", false},
		{"IPv6 translation", []string{"64:ff9b::a9fe:a9fe"}, "example.com", false},
		{"unspecified", []string{"0.0.0.0"}, "example.com", false},
		{"multicast", []string{"224.0.0.1"}, "example.com", false},
		{"explicit loopback", nil, "http://127.0.0.1:8080", true},
		{"explicit private", nil, "http://10.0.0.1:8080", true},
		{"explicit metadata forbidden", nil, "http://169.254.169.254:80", false},
		{"localhost resolved local", []string{"127.0.0.1", "::1"}, "http://localhost:8080", true},
		{"localhost resolved remote", []string{"93.184.216.34"}, "http://localhost:8080", false},
		{"no DNS results", nil, "example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTransport([]string{tc.allowed}).(*transport)
			lookups, dials := 0, 0
			tr.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
				lookups++
				var addresses []netip.Addr
				for _, ip := range tc.addresses {
					addresses = append(addresses, netip.MustParseAddr(ip))
				}
				return addresses, nil
			}
			var dialed string
			tr.connect = func(_ context.Context, _, address string) (net.Conn, error) {
				dials++
				dialed = address
				client, server := net.Pipe()
				server.Close()
				return client, nil
			}
			u := tc.allowed
			if !strings.Contains(u, "://") {
				u = "https://" + u
			}
			parsed, _ := url.Parse(u)
			port := parsed.Port()
			if port == "" {
				port = "443"
			}
			conn, err := tr.dial(t.Context(), "tcp", net.JoinHostPort(parsed.Hostname(), port))
			if conn != nil {
				conn.Close()
			}
			if tc.pass {
				if err != nil || dials != 1 {
					t.Fatalf("allowed dial: %v, dials=%d", err, dials)
				}
				host, _, _ := net.SplitHostPort(dialed)
				if _, err := netip.ParseAddr(host); err != nil {
					t.Fatalf("unpinned DNS address: %q", dialed)
				}
				if lookups > 1 {
					t.Fatalf("second DNS lookup: %d", lookups)
				}
			} else if !errors.Is(err, ErrDenied) || dials != 0 {
				t.Fatalf("unsafe dial: %v, dials=%d", err, dials)
			}
		})
	}
}

func TestTransportUsesDirectConnectionAndDeniesUnsafeRequests(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer s.Close()
	tr := NewTransport([]string{s.URL}).(*transport)
	defer tr.CloseIdleConnections()
	response, err := (&http.Client{Transport: tr}).Get(s.URL)
	if err != nil || response.StatusCode != 204 {
		t.Fatalf("direct request failed: %v", err)
	}
	response.Body.Close()
	if tr.http.Proxy != nil {
		t.Fatal("environment proxy enabled")
	}
	request, _ := http.NewRequest("GET", "http://127.0.0.1:1", nil)
	if _, err := tr.RoundTrip(request); !errors.Is(err, ErrDenied) {
		t.Fatalf("wrong target: %v", err)
	}
	request.URL, _ = url.Parse(s.URL)
	request.Header.Set("Authorization", "should-not-send")
	// The transport governs targets, not trusted credential injection; caller owns headers.
	request.URL.User = url.UserPassword("user", "password")
	if _, err := tr.RoundTrip(request); !errors.Is(err, ErrDenied) {
		t.Fatalf("URL credentials: %v", err)
	}
}

func TestDialErrorsAndCancellation(t *testing.T) {
	tr := NewTransport([]string{"example.com"}).(*transport)
	if _, err := tr.dial(t.Context(), "tcp", "invalid"); !errors.Is(err, ErrDenied) {
		t.Fatalf("invalid address: %v", err)
	}
	if _, err := tr.dial(t.Context(), "tcp", "other.example:443"); !errors.Is(err, ErrDenied) {
		t.Fatalf("unknown address: %v", err)
	}
	tr.lookup = func(context.Context, string, string) ([]netip.Addr, error) { return nil, context.Canceled }
	if _, err := tr.dial(t.Context(), "tcp", "example.com:443"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lookup cancellation: %v", err)
	}
	tr.lookup = func(context.Context, string, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
	}
	tr.connect = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if _, err := tr.dial(t.Context(), "tcp", "example.com:443"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial deadline: %v", err)
	}
}
