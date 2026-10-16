package filesystem

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
)

// ValidatePlanSources reopens explicitly observed text through the same scoped,
// no-follow reader. This is a startup drift check, not an atomic filesystem view.
func (s *Service) ValidatePlanSources(ctx context.Context, sources []domain.PlanSource) error {
	for _, source := range sources {
		if source.RootRevision != s.revision {
			return errors.New("plan filesystem root configuration changed; create a new plan")
		}
		root, err := s.authorizedRoot(ctx, source.Root)
		if err != nil {
			return err
		}
		if err := validatePath(source.Path); err != nil {
			return err
		}
		file, err := s.readText(ctx, root, source.Path, maxFileBytes)
		if err != nil {
			return err
		}
		if file.hash != source.ContentHash || file.identity != source.FileID {
			return errors.New("plan source changed; create a new plan")
		}
	}
	return nil
}
