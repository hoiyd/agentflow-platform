package egress

import (
	"context"
	"errors"
	"net"
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
