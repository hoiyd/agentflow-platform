package store

import (
	"encoding/json"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

// SaveToolEffectIntent retains bounded non-sensitive evidence before publishing
// a nontransactional effect. The existing result column becomes the final
// ExecutionResult only after journal commit; no second receipt store is needed.
func (s *PostgresStore) SaveToolEffectIntent(key string, intent []byte) error {
	if len(intent) == 0 || len(intent) > 12000 || !json.Valid(intent) {
		return errors.New("invalid bounded Tool effect intent")
	}
	safe, _, err := redaction.JSON(intent)
	if err != nil {
		return err
	}
	_, err = s.updateToolEffect(key, func(existing domain.ToolEffectRecord) (domain.ToolEffectStatus, []byte, string, error) {
		if existing.Status != domain.ToolEffectExecuting || len(existing.Result) != 0 {
			return "", nil, "", errors.New("Tool effect cannot replace prepared evidence")
		}
		return existing.Status, safe, existing.Error, nil
	})
	return err
}

// FailToolEffect settles a confirmed pre-publication rejection, never a timeout
// or an attempt that may already have published an external effect.
func (s *PostgresStore) FailToolEffect(key, message string) error {
	message, _ = redaction.Text(strings.TrimSpace(message))
	_, err := s.updateToolEffect(key, func(existing domain.ToolEffectRecord) (domain.ToolEffectStatus, []byte, string, error) {
		if existing.Status != domain.ToolEffectExecuting {
			return "", nil, "", errors.New("Tool effect execution ownership was lost")
		}
		return domain.ToolEffectFailed, existing.Result, message, nil
	})
	return err
}
