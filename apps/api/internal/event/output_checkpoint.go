package event

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

const outputCheckpointInterval = time.Second
const outputCheckpointBytes = 16 * 1024

type OutputCheckpointPayload domain.PartialOutput

func (OutputCheckpointPayload) supports(t domain.RunEventType) bool {
	return t == domain.EventModelOutputCheckpoint
}

// OutputRecorder coalesces display changes into the existing durable event
// stream. Observers receive committed replacements, not unrecoverable deltas.
// The initiating response may be ahead by the pending checkpoint window.
type OutputRecorder struct {
	mu          sync.Mutex
	sink        Sink
	cancel      func()
	stop        chan struct{}
	done        chan struct{}
	closed      bool
	err         error
	items       []*outputBuffer
	round       int
	modelCallID string
}

type outputBuffer struct {
	key          string
	meta         domain.RunEvent
	value        domain.PartialOutput
	raw          string
	dirty        bool
	pendingBytes int
	committed    domain.PartialOutput
	sanitized    bool
}

func NewOutputRecorder(sink Sink, cancel func()) *OutputRecorder {
	r := &OutputRecorder{sink: sink, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		ticker := time.NewTicker(outputCheckpointInterval)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				r.mu.Lock()
				if !r.closed {
					_ = r.flushLocked("")
				}
				r.mu.Unlock()
			}
		}
	}()
	return r
}

func (r *OutputRecorder) Publish(ctx context.Context, item domain.RunEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	if r.closed {
		return fmt.Errorf("partial output recorder is closed")
	}
	switch item.Type {
	case domain.EventModelDelta, domain.EventModelReasoningDelta, domain.EventModelReasoning:
		if err := r.acceptLocked(item); err != nil {
			return err
		}
		// Keep the existing terminal reasoning contract. Its live prefixes are
		// instead exposed through durable output replacements to subscribers.
		if item.Type != domain.EventModelReasoning {
			return nil
		}
	case domain.EventTurnCompleted:
		if err := r.flushLocked("final"); err != nil {
			return err
		}
	case domain.EventTurnFailed, domain.EventTurnCanceled:
		if err := r.flushLocked("interrupted"); err != nil {
			return err
		}
	}
	return r.sink.Publish(ctx, item)
}

func (r *OutputRecorder) acceptLocked(item domain.RunEvent) error {
	channel, key := "answer", "answer"
	modelCall, _ := item.Payload["model_call_id"].(string)
	if r.round == 0 || (modelCall != "" && modelCall != r.modelCallID) {
		r.round++
		r.modelCallID = modelCall
	}
	attempt := outputInt(item.Payload["attempt"])
	if item.Type != domain.EventModelDelta {
		channel, key = "reasoning", "reasoning:"+modelCall
	}
	var current *outputBuffer
	for _, entry := range r.items {
		if entry.key == key {
			current = entry
			break
		}
	}
	if current == nil {
		if len(r.items) == domain.MaxPartialOutputEntries {
			drop := 0
			if r.items[0].key == "answer" {
				drop = 1
			}
			r.items = append(r.items[:drop], r.items[drop+1:]...)
		}
		current = &outputBuffer{key: key, value: domain.PartialOutput{Channel: channel}}
		r.items = append(r.items, current)
	}
	current.meta = item
	current.value.RunID, current.value.TurnID, current.value.StageID = item.RunID, item.TurnID, item.StageID
	current.value.Role, _ = item.Payload["role"].(string)
	if (current.committed.Revision > 0 || current.raw != "") && (current.value.ModelCallID != modelCall || current.value.Attempt != attempt) {
		current.raw = ""
		current.sanitized = false
		current.value.Truncated = false
	}
	current.value.ModelCallID, current.value.Attempt = modelCall, attempt
	reset, _ := item.Payload["reset"].(bool)
	status, _ := item.Payload["status"].(string)
	if channel == "answer" {
		if reset {
			current.raw = ""
			current.sanitized = false
			if modelCall == "" {
				r.round++
			}
			current.value.Truncated = false
		}
		delta, _ := item.Payload["delta"].(string)
		remaining := domain.MaxPartialOutputBytes + 4096 - len(current.raw)
		if len(delta) > remaining {
			delta = utf8Prefix(delta, remaining)
			current.value.Truncated = true
		}
		if display, ok := item.Payload["display_text"].(string); ok {
			current.pendingBytes += max(0, len(display)-len(current.raw))
			current.raw, current.sanitized = utf8Prefix(display, domain.MaxPartialOutputBytes), true
			if truncated, _ := item.Payload["display_truncated"].(bool); truncated {
				current.value.Truncated = true
			}
		} else {
			current.pendingBytes += len(delta)
			current.raw += delta
		}
	} else {
		text, _ := item.Payload["text"].(string)
		if text != "" {
			current.pendingBytes += max(0, len(text)-len(current.raw))
			current.raw = text
		}
		if truncated, _ := item.Payload["truncated"].(bool); truncated {
			current.value.Truncated = true
		}
	}
	current.value.Round = r.round
	current.value.Status = "provisional"
	if reset {
		current.value.Status = "retracted"
	}
	if status == "complete" {
		current.value.Status = "final"
	}
	if status == "interrupted" {
		current.value.Status = "interrupted"
	}
	current.dirty = true
	if reset || status == "complete" || status == "interrupted" || current.committed.Revision == 0 || current.pendingBytes >= outputCheckpointBytes {
		return r.flushLocked("")
	}
	return nil
}

func (r *OutputRecorder) flushLocked(status string) error {
	if r.err != nil {
		return r.err
	}
	for _, item := range r.items {
		if status != "" && item.value.Status == "provisional" {
			item.value.Status = status
			item.dirty = true
		}
		if !item.dirty {
			continue
		}
		text := item.raw
		// Whole-prefix redaction cannot safely publish an unfinished token.
		// ponytail: unbroken answer text waits for whitespace or terminal flush.
		if item.value.Channel == "answer" && !item.sanitized && item.value.Status == "provisional" {
			end := strings.LastIndexFunc(text, unicode.IsSpace)
			if end < 0 {
				text = ""
			} else {
				text = text[:end]
			}
		}
		text, _ = redaction.Text(text)
		if start := strings.Index(text, "-----BEGIN "); start >= 0 {
			text = text[:start] + "[REDACTED]"
		}
		limit := domain.MaxPartialOutputBytes
		if item.value.Channel == "reasoning" {
			limit = domain.MaxPartialReasoningBytes
		}
		if len(text) > limit {
			text = utf8Prefix(text, limit)
			item.value.Truncated = true
		}
		text = strings.TrimSpace(text)
		if text == "" && item.committed.Revision == 0 && item.value.Status == "provisional" {
			continue
		}
		item.value.Text, item.value.Offset = text, len(text)
		if item.value == item.committed {
			item.dirty = false
			item.pendingBytes = 0
			continue
		}
		item.value.Revision++
		checkpoint, err := NewRunEvent(domain.EventModelOutputCheckpoint, EventMetadata{
			RunID: item.meta.RunID, ConversationID: item.meta.ConversationID, StageID: item.meta.StageID,
			TurnID: item.meta.TurnID, ParentEventID: item.meta.ParentEventID,
		}, OutputCheckpointPayload(item.value))
		if err != nil {
			return r.failLocked(err)
		}
		if err = r.sink.Publish(context.Background(), checkpoint); err != nil {
			return r.failLocked(fmt.Errorf("persist partial output: %w", err))
		}
		item.dirty, item.committed, item.pendingBytes = false, item.value, 0
	}
	return nil
}

func (r *OutputRecorder) failLocked(err error) error {
	r.err = err
	if r.cancel != nil {
		r.cancel()
	}
	return err
}

func (r *OutputRecorder) Flush(status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked(status)
}

func (r *OutputRecorder) Close() error {
	r.mu.Lock()
	if !r.closed {
		_ = r.flushLocked("interrupted")
		r.closed = true
		close(r.stop)
	}
	err := r.err
	r.mu.Unlock()
	<-r.done
	return err
}

func utf8Prefix(text string, limit int) string {
	if limit >= len(text) {
		return text
	}
	if limit < 0 {
		limit = 0
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func outputInt(value any) int {
	switch n := value.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
