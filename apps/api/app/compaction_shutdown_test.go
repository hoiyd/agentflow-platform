package app

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/store"
)

// Failure inventory: Store must remain usable during settlement and remain open
// if a background task did not drain, rather than reporting a successful Close.
func TestCompactionDrainBeforeStoreClose(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "settled", true: "timeout"}[fail], func(t *testing.T) {
			storage := &compactionCloseStore{}
			a := &Application{store: storage, compaction: compactionCloserFunc(func(context.Context) error {
				if storage.closed {
					t.Fatal("Store closed before auxiliary settlement")
				}
				if fail {
					return context.DeadlineExceeded
				}
				return nil
			})}
			err := a.Close(context.Background())
			if fail {
				if !errors.Is(err, context.DeadlineExceeded) || storage.closed {
					t.Fatalf("closed=%v err=%v", storage.closed, err)
				}
			} else if err != nil || !storage.closed {
				t.Fatalf("closed=%v err=%v", storage.closed, err)
			}
		})
	}
}

type compactionCloseStore struct {
	store.Store
	closed bool
}

func (s *compactionCloseStore) Close() error { s.closed = true; return nil }

type compactionCloserFunc func(context.Context) error

func (f compactionCloserFunc) CloseCompaction(ctx context.Context) error { return f(ctx) }
