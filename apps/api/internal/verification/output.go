package verification

import "bytes"

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
	total  int
}

func newCappedBuffer(limit int) *cappedBuffer {
	if limit <= 0 {
		limit = defaultArtifactBytes
	}
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	b.total += len(value)
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
