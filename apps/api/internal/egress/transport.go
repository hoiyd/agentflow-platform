// Package egress enforces destination policy at the HTTP connection boundary.
// It is not an OS sandbox; callers still own methods, credentials and body limits.
package egress

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrDenied = errors.New("network destination is not permitted")

type transport struct {
	http    *http.Transport
	origins map[string]bool
	// Dial policy is indexed separately because net/http passes host:port, not a URL.
	local   map[string]bool
	lookup  func(context.Context, string, string) ([]netip.Addr, error)
	connect func(context.Context, string, string) (net.Conn, error)
}

// NewTransport accepts exact origins. A bare public hostname means HTTPS:443.
// Local/private access requires an explicit URL with a literal IP or localhost.
// Malformed grants authorize nothing. Proxies and redirects must not bypass it.
func NewTransport(allowed []string) http.RoundTripper {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	t := &transport{
		origins: make(map[string]bool), local: make(map[string]bool),
		lookup: net.DefaultResolver.LookupNetIP, connect: dialer.DialContext,
	}
	for _, value := range allowed {
		value = strings.TrimSpace(value)
		explicit := strings.Contains(value, "://")
		if !explicit {
			value = "https://" + value
		}
		u, err := url.Parse(value)
		if err != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || (!explicit && u.Port() != "") {
			continue
		}
		key := origin(u)
		if key == "" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		ip, _ := netip.ParseAddr(host)
		local := ip.IsValid() && (ip.Unmap().IsPrivate() || ip.Unmap().IsLoopback()) || host == "localhost"
		if local && !explicit {
			continue
		}
		t.origins[key] = true
		t.local[strings.TrimPrefix(key, u.Scheme+"://")] = local
	}
	t.http = &http.Transport{
		DialContext: t.dial, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout: 30 * time.Second, MaxIdleConns: 8, MaxIdleConnsPerHost: 2,
	}
	return t
}

func origin(u *url.URL) string {
	if u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Opaque != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.ContainsAny(host, "%*\\") {
		return ""
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return ""
	}
	return u.Scheme + "://" + net.JoinHostPort(host, strconv.Itoa(n))
}

func (t *transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if !t.origins[origin(r.URL)] {
		return nil, ErrDenied
	}
	return t.http.RoundTrip(r)
}

func (t *transport) CloseIdleConnections() { t.http.CloseIdleConnections() }

func (t *transport) dial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, ErrDenied
	}
	host = strings.ToLower(host)
	allowLocal, allowed := t.local[net.JoinHostPort(host, port)]
	if !allowed {
		return nil, ErrDenied
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = t.lookup(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(ips) == 0 {
		return nil, ErrDenied
	}
	for _, ip := range ips {
		ip = ip.Unmap()
		local := allowLocal && (ip.IsPrivate() || ip.IsLoopback())
		if host == "localhost" {
			local = allowLocal && ip.IsLoopback()
		}
		if !local && !publicIP(ip) {
			return nil, ErrDenied
		}
		if host == "localhost" && !local {
			return nil, ErrDenied
		}
	}
	// Resolve once, validate every answer, then dial literals. TLS still verifies
	// the original hostname; no insecure TLS setting or second DNS lookup is used.
	var last error
	for _, ip := range ips {
		conn, err := t.connect(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, last
}

var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func publicIP(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range reserved {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
