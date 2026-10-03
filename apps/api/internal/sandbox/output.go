package sandbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"strings"
	"sync"
)

type outputBuffer struct {
	mu      sync.Mutex
	content bytes.Buffer
	total   int64
	limit   int
	hash    hash.Hash
}

func newOutputBuffer(limit int) *outputBuffer { return &outputBuffer{limit: limit, hash: sha256.New()} }
func (b *outputBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(data))
	_, _ = b.hash.Write(data)
	if remaining := b.limit - b.content.Len(); remaining > 0 {
		_, _ = b.content.Write(data[:min(len(data), remaining)])
	}
	return len(data), nil
}
func (b *outputBuffer) Text() string {
	// ASCII replacement cannot expand binary bytes beyond the retention cap.
	return strings.ReplaceAll(strings.ToValidUTF8(b.content.String(), "?"), "\x00", "?")
}
func (b *outputBuffer) Hash() string    { return "sha256:" + hex.EncodeToString(b.hash.Sum(nil)) }
func (b *outputBuffer) Truncated() bool { return b.total > int64(b.content.Len()) }
