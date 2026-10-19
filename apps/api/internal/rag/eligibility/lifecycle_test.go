package eligibility

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

// Failure inventory: exact start is included, exact end is excluded; missing
// legacy fields stay eligible; malformed windows and inactive status fail closed.
func TestLifecycleBoundaries(t *testing.T) {
	at := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		lifecycle domain.DocumentLifecycle
		want      bool
	}{
		{"legacy", domain.DocumentLifecycle{}, true},
		{"start", domain.DocumentLifecycle{ValidFrom: at.Format(time.RFC3339)}, true},
		{"before_start", domain.DocumentLifecycle{ValidFrom: at.Add(time.Microsecond).Format(time.RFC3339Nano)}, false},
		{"end", domain.DocumentLifecycle{ValidUntil: at.Format(time.RFC3339)}, false},
		{"before_end", domain.DocumentLifecycle{ValidUntil: at.Add(time.Microsecond).Format(time.RFC3339Nano)}, true},
		{"draft", domain.DocumentLifecycle{Status: "draft"}, false},
		{"withdrawn", domain.DocumentLifecycle{Status: "withdrawn"}, false},
		{"unknown", domain.DocumentLifecycle{Status: "unknown"}, false},
		{"invalid", domain.DocumentLifecycle{ValidUntil: "tomorrow"}, false},
		{"reversed", domain.DocumentLifecycle{ValidFrom: "2027-01-01T00:00:00Z", ValidUntil: "2026-01-01T00:00:00Z"}, false},
		{"publication_not_gate", domain.DocumentLifecycle{PublishedAt: "2099-01-01T00:00:00Z"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := CompileAt(nil, at)
			if err != nil {
				t.Fatal(err)
			}
			// Chunk labels cannot widen or narrow the Document lifecycle.
			got := p.Matches(domain.Document{Lifecycle: tc.lifecycle}, domain.DocumentChunk{Metadata: map[string]any{"status": "active", "valid_until": "9999-01-01T00:00:00Z"}})
			if got != tc.want {
				t.Fatalf("eligible=%v want %v", got, tc.want)
			}
		})
	}
}
