package fixturestore

import (
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (s *Store) TransitionRun(request domain.RunTransition) (domain.RunTransitionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, run := range s.data.Runs {
		if run.ID != request.RunID {
			continue
		}
		var cursor int64
		for _, event := range s.data.RunEvents {
			if event.RunID == run.ID {
				cursor = max(cursor, event.Sequence)
			}
		}
		result, err := store.PrepareRunTransition(store.CloneRun(run), request, cursor, time.Now().UTC())
		if err == nil && result.Applied {
			s.data.Runs[i] = store.CloneRun(result.Run)
			s.data.RunEvents = append(s.data.RunEvents, result.Events...)
		}
		return result, err
	}
	return domain.RunTransitionResult{}, store.ErrNotFound("run")
}
