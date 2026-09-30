package runcompletion

import (
	"strings"
	"testing"
)

func TestModelOutputMatchesAnswerWithBoundedTraceOutput(t *testing.T) {
	answer := strings.Repeat("a", 20_100) + " [W1]"
	if !modelOutputMatchesAnswer(map[string]any{
		"output": answer[:20_000] + "...[truncated]", "output_chars": len(answer),
	}, answer) {
		t.Fatal("bounded model trace rejected its full answer")
	}
	if modelOutputMatchesAnswer(map[string]any{
		"output": answer[:20_000] + "...[truncated]", "output_chars": len(answer) + 1,
	}, answer) {
		t.Fatal("mismatched model output length was accepted")
	}
}
