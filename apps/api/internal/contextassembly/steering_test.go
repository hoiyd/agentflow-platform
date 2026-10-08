package contextassembly

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestSteeringRequiredDeduplicatedAndLocated(t *testing.T) {
	ctx := WithSession(context.Background(), Session{Config: DefaultConfig(), LoadSteering: func() ([]domain.RunInput, error) {
		return []domain.RunInput{{ID: "input-one", Content: "Use metric units"}}, nil
	}})
	// Already persisted history and same-Turn replay must not duplicate steering.
	raw := []Message{{Role: "system", Content: "Assistant"}, {Role: "user", Content: "Answer"}, {Role: "user", Source: SourceHistory, ReferenceID: "input-one", Content: "Use metric units"}}
	pack, err := Assemble(ctx, Request{Model: "test", Messages: raw})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range pack.Manifest.Entries {
		if entry.ReferenceID == "input-one" {
			count++
			if !entry.Selected || entry.Source != SourceSteering {
				t.Fatalf("lost steering: %+v", entry)
			}
		}
	}
	if count != 1 {
		t.Fatalf("duplicate steering: %+v", pack.Manifest.Entries)
	}
	failure := errors.New("inbox unavailable")
	ctx = WithSession(ctx, Session{LoadSteering: func() ([]domain.RunInput, error) { return nil, failure }})
	if _, err = Assemble(ctx, Request{Messages: raw}); !errors.Is(err, failure) {
		t.Fatalf("ignored receipt failure: %v", err)
	}
}
