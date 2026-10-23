package tool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The cancellation starts only after response headers arrive; TLS/DNS startup
// speed cannot decide whether the intended body-read boundary was exercised.
func TestWebFetchHTTPBodyFailureIntegration(t *testing.T) {
	for _, scenario := range []string{"cancel-after-headers", "short-body"} {
		t.Run(scenario, func(t *testing.T) {
			headers := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				if scenario == "short-body" {
					w.Header().Set("Content-Length", "1000")
					_, _ = io.WriteString(w, "incomplete body")
					return
				}
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				close(headers)
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				_, err := fetchFixtureBinding(server).Handler(ctx, json.RawMessage(`{"source_id":"W1"}`))
				finished <- err
			}()
			if scenario == "cancel-after-headers" {
				select {
				case <-headers:
					cancel()
				case <-ctx.Done():
					t.Fatal("response headers never arrived")
				}
			}
			select {
			case err := <-finished:
				var typed *ExecutionError
				want := ErrorResultEncoding
				if scenario == "cancel-after-headers" {
					want = ErrorExecutionCanceled
				}
				if !errors.As(err, &typed) || typed.Code != want {
					t.Fatalf("body failure=%v want=%s", err, want)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("body request did not stop")
			}
		})
	}
}
