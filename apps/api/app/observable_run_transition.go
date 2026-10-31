package app

import "agentflow-platform/apps/api/internal/domain"

func (s *observableStore) TransitionRun(request domain.RunTransition) (domain.RunTransitionResult, error) {
	result, err := s.Store.TransitionRun(request)
	if err == nil {
		for _, event := range result.Events {
			s.publishCommitted(event)
		}
	}
	return result, err
}
