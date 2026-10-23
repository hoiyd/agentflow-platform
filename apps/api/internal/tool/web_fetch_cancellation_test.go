package tool

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Model the boundary directly: cancellation and the final body read happen
// together, without depending on socket shutdown or scheduler timing.
func TestWebFetchCancellationWinsOverBodyCompletion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		encoding string
		body     string
		readErr  error
	}{
		{name: "empty-eof", readErr: io.EOF},
		{name: "complete-body", body: "Readable article evidence", readErr: io.EOF},
		{name: "short-body", readErr: io.ErrUnexpectedEOF},
		{name: "gzip-header", encoding: "gzip", readErr: io.EOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			binding := webFetchTool(func(context.Context, string) (domain.WebCitation, error) {
				return domain.WebCitation{SourceID: "W1", URL: "https://example.com/article"}, nil
			}, func(string) http.RoundTripper {
				return tavilyRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusOK, Request: request,
						Header: http.Header{"Content-Type": {"text/plain"}, "Content-Encoding": {tc.encoding}},
						Body: io.NopCloser(webFetchBodyReader(func(buffer []byte) (int, error) {
							cancel()
							return copy(buffer, tc.body), tc.readErr
						})),
					}, nil
				})
			})
			_, err := binding.Handler(ctx, json.RawMessage(`{"source_id":"W1"}`))
			var typed *ExecutionError
			if !errors.As(err, &typed) || typed.Code != ErrorExecutionCanceled || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled read=%v; want execution_canceled wrapping context.Canceled", err)
			}
		})
	}
}

type webFetchBodyReader func([]byte) (int, error)

func (read webFetchBodyReader) Read(buffer []byte) (int, error) { return read(buffer) }
