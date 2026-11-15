package loadtest

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

type providerFixture struct {
	server        *httptest.Server
	delay         time.Duration
	longTailEvery int64
	requests      atomic.Int64
	active        atomic.Int64
	peak          atomic.Int64
	connections   atomic.Int64
	opened        atomic.Int64
}

func newProviderFixture(delay time.Duration, longTailEvery int64) *providerFixture {
	fixture := &providerFixture{delay: delay, longTailEvery: longTailEvery}
	server := httptest.NewUnstartedServer(http.HandlerFunc(fixture.serveHTTP))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			fixture.opened.Add(1)
			fixture.connections.Add(1)
		case http.StateClosed, http.StateHijacked:
			fixture.connections.Add(-1)
		}
	}
	server.Start()
	fixture.server = server
	return fixture
}

func (p *providerFixture) serveHTTP(w http.ResponseWriter, request *http.Request) {
	active := p.active.Add(1)
	updatePeak(&p.peak, active)
	defer p.active.Add(-1)

	body, _ := io.ReadAll(request.Body)
	if bytes.Contains(body, []byte("provider-429")) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture capacity","type":"rate_limit_error","code":"rate_limit_exceeded"}}`)
		return
	}
	if bytes.Contains(body, []byte("provider-503")) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"fixture slot unavailable"}}`)
		return
	}

	delay := p.delay
	requestNumber := p.requests.Add(1)
	if bytes.Contains(body, []byte("ignore-cancel")) {
		time.Sleep(p.delay * 4)
	} else {
		if p.longTailEvery > 0 && requestNumber%p.longTailEvery == 0 {
			delay *= 4
		}
		select {
		case <-request.Context().Done():
			return
		case <-time.After(delay):
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":8,"completion_tokens":4,"total_tokens":12}}`)
}

func (p *providerFixture) close() {
	p.server.CloseClientConnections()
	p.server.Close()
}

func waitForProviderIdle(provider *providerFixture, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for provider.active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	return provider.active.Load() == 0
}
