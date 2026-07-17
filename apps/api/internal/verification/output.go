package verification

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
	total  int
	hash   hash.Hash
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit <= 0 {
		limit = defaultArtifactBytes
	}
	return &cappedBuffer{limit: limit, hash: sha256.New()}
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	b.total += len(value)
	_, _ = b.hash.Write(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) < remaining {
			remaining = len(value)
		}
		_, _ = b.buffer.Write(value[:remaining])
	}
	return len(value), nil
}

func (b *cappedBuffer) String() string  { return b.buffer.String() }
func (b *cappedBuffer) Total() int      { return b.total }
func (b *cappedBuffer) Truncated() bool { return b.total > b.buffer.Len() }
func (b *cappedBuffer) Hash() string {
	return "sha256:" + hex.EncodeToString(b.hash.Sum(nil))
}
