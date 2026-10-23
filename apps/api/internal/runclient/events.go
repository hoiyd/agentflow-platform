package runclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/apicontract"
)

const maxFrameBytes = 2 << 20

type frame struct{ event, id, data string }

func formatSequence(value int64) string { return strconv.FormatInt(value, 10) }

// A cursor is acknowledged only after a blank-line-terminated frame reaches
// the consumer. Partial EOF must never turn into an acknowledged event.
func frames(reader io.Reader, visit func(frame) (bool, error)) error {
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), maxFrameBytes)
	var next frame
	size := 0
	for scan.Scan() {
		line := scan.Text()
		size += len(line) + 1
		if size > maxFrameBytes {
			return problem("protocol_error", "SSE frame exceeds 2 MiB")
		}
		if line == "" {
			if next.data != "" {
				stop, err := visit(next)
				if err != nil {
					return err
				}
				if stop {
					return nil
				}
			}
			next = frame{}
			size = 0
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch key {
		case "event":
			next.event = value
		case "id":
			next.id = value
		case "data":
			if next.data != "" {
				next.data += "\n"
			}
			next.data += value
		}
	}
	if scan.Err() != nil {
		return problem("stream_lost", "SSE stream interrupted or exceeded its line limit")
	}
	return io.ErrUnexpectedEOF
}

type Receipt struct {
	RunID          string `json:"run_id"`
	ConversationID string `json:"conversation_id,omitempty"`
	RequestSHA256  string `json:"request_sha256,omitempty"`
}

func (c *Client) Start(ctx context.Context, request apicontract.ChatRequest) (Receipt, error) {
	if ctx.Err() != nil {
		return Receipt{}, ctx.Err()
	}
	data, err := json.Marshal(request)
	if err != nil {
		return Receipt{}, problem("invalid_options", "request cannot be encoded")
	}
	if len(data) > 2<<20 {
		return Receipt{}, problem("invalid_options", "ChatRequest exceeds 2 MiB")
	}
	receipt := Receipt{RequestSHA256: digest(data)}
	response, err := c.request(ctx, http.MethodPost, "/api/chat", data, 0)
	if err != nil {
		var detail *Error
		if errors.As(err, &detail) && detail.Status >= 400 && detail.Status < 500 {
			return receipt, err
		}
		uncertain := unknown(receipt)
		if errors.As(err, &detail) {
			uncertain.Status, uncertain.RetryAfter = detail.Status, detail.RetryAfter
		}
		return receipt, uncertain
	}
	defer response.Body.Close()
	if !isEventStream(response) {
		return receipt, unknown(receipt)
	}
	err = frames(response.Body, func(item frame) (bool, error) {
		if item.event == "conversation" {
			var value struct {
				ConversationID string `json:"conversation_id"`
			}
			if json.Unmarshal([]byte(item.data), &value) == nil {
				receipt.ConversationID = value.ConversationID
			}
			return false, nil
		}
		var event apicontract.RunEvent
		if json.Unmarshal([]byte(item.data), &event) == nil && identifier.MatchString(event.RunId) {
			receipt.RunID = event.RunId
			if event.ConversationId != nil {
				receipt.ConversationID = *event.ConversationId
			}
			return true, nil
		}
		return false, nil
	})
	if err != nil || receipt.RunID == "" {
		return receipt, unknown(receipt)
	}
	return receipt, nil
}

func unknown(receipt Receipt) *Error {
	return &Error{Code: "submission_unknown", Message: "Submission may have created a Run; inspect the Conversation/Recent runs before retrying", ConversationID: receipt.ConversationID, RequestSHA256: receipt.RequestSHA256}
}
func isEventStream(r *http.Response) bool {
	return strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]), "text/event-stream")
}

func (c *Client) Observe(ctx context.Context, id string, after int64, out io.Writer) (int64, error) {
	path, err := runPath(id, "/events")
	if err != nil {
		return after, err
	}
	if after < 0 {
		return after, problem("invalid_options", "after must be non-negative")
	}
	encoder := json.NewEncoder(out)
	for attempt := 0; attempt < 6; attempt++ {
		if ctx.Err() != nil {
			return after, ctx.Err()
		}
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt) * 100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return after, ctx.Err()
			case <-timer.C:
			}
		}
		response, err := c.request(ctx, http.MethodGet, path, nil, after)
		if err != nil {
			var detail *Error
			if errors.As(err, &detail) && (detail.Status == 0 || detail.Status == 502 || detail.Status == 503 || detail.Status == 504) {
				continue
			}
			return after, err
		}
		if !isEventStream(response) {
			response.Body.Close()
			return after, problem("protocol_error", "expected an SSE event stream")
		}
		stopped := false
		err = frames(response.Body, func(item frame) (bool, error) {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			if item.event == "error" {
				var detail struct {
					Code string `json:"code"`
				}
				if json.Unmarshal([]byte(item.data), &detail) != nil {
					return false, problem("protocol_error", "invalid SSE error")
				}
				if detail.Code == "event_subscription_lost" || detail.Code == "event_subscriber_lagged" {
					return false, problem("stream_lost", "observer lagged; reconnecting")
				}
				return false, problem("stream_rejected", "observation authorization or execution failed; read the Run before retrying")
			}
			if item.event == "run.snapshot" {
				var snapshot apicontract.RunProjectionSnapshot
				if json.Unmarshal([]byte(item.data), &snapshot) != nil || snapshot.Run.RunId != id || snapshot.AsOfSequence != after {
					return false, problem("protocol_error", "snapshot identity or durable sequence mismatch")
				}
				if err := encoder.Encode(map[string]any{"kind": "snapshot", "after": after, "snapshot": snapshot}); err != nil {
					return false, problem("output_failed", "cannot write observation output")
				}
				stopped = observationStopped(string(snapshot.Run.Status))
				return stopped, nil
			}
			var event apicontract.RunEvent
			if json.Unmarshal([]byte(item.data), &event) != nil || event.RunId != id || event.Type != item.event {
				return false, problem("protocol_error", "event identity/type mismatch")
			}
			if event.Sequence < 0 {
				return false, problem("protocol_error", "negative event sequence")
			}
			if event.Sequence > 0 {
				sequence, err := strconv.ParseInt(item.id, 10, 64)
				if err != nil || sequence != event.Sequence {
					return false, problem("protocol_error", "event cursor mismatch")
				}
				if sequence <= after {
					return false, nil
				}
				if sequence != after+1 {
					return false, problem("stream_lost", "durable sequence gap; reconnecting")
				}
			}
			if err := encoder.Encode(map[string]any{"kind": "event", "event": event}); err != nil {
				return false, problem("output_failed", "cannot write observation output")
			}
			if event.Sequence > 0 {
				after = event.Sequence
			}
			stopped = event.Type == "run.completed" || event.Type == "run.failed" || event.Type == "run.canceled" || event.Type == "run.waiting_for_user"
			return stopped, nil
		})
		response.Body.Close()
		if stopped && err == nil {
			return after, nil
		}
		if ctx.Err() != nil {
			return after, ctx.Err()
		}
		var detail *Error
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && (!errors.As(err, &detail) || detail.Code != "stream_lost") {
			return after, err
		}
	}
	return after, problem("observation_lost", "reconnect limit reached; observe again with this Run ID and cursor")
}

func observationStopped(status string) bool {
	switch status {
	case "completed", "failed", "failed_recoverable", "canceled", "waiting_for_user":
		return true
	}
	return false
}
