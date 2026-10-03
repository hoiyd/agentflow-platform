package sandbox

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestOutputSanitizationCannotExpandRetentionLimit(t *testing.T) {
	for _, raw := range [][]byte{{0, 0, 0}, {0xff, 0xfe, 0xfd}, []byte("long output"), []byte("\xe4\xb8\xad\xe6\x96\x87")} {
		output := newOutputBuffer(3)
		if _, err := output.Write(raw); err != nil {
			t.Fatal(err)
		}
		text := output.Text()
		if len(text) > 3 || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
			t.Fatalf("invalid/beyond-cap text %q", text)
		}
		if output.Hash() != fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) || output.total != int64(len(raw)) || output.Truncated() != (len(raw) > 3) {
			t.Fatal("raw evidence changed during sanitization")
		}
	}
}
