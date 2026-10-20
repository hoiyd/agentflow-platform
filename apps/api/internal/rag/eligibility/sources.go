package eligibility

import (
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

var ErrSourceUnavailable = failure.New(failure.Definition{
	Message: "knowledge source is unavailable, changed, or no longer eligible; start a new Run to search current sources",
	Info:    failure.Info{Code: "knowledge_source_unavailable", Source: "knowledge", Category: failure.CategoryValidation},
})

// ValidateSources checks current rows, never rewriting the historical citation.
// Call before reusing persisted evidence or committing a newly generated answer.
func ValidateSources(snapshot *domain.RuntimeSnapshot, workspace string, sources []domain.RAGCitation, load func(string) (domain.Document, []domain.DocumentChunk, bool, error)) error {
	if len(sources) == 0 {
		return nil
	}
	filter, err := FrozenFilter(snapshot)
	if err != nil {
		return err
	}
	predicate, err := Compile(filter)
	if err != nil {
		return err
	}
	type record struct {
		document domain.Document
		chunks   []domain.DocumentChunk
	}
	loaded := map[string]record{}
	for _, source := range sources {
		row, exists := loaded[source.DocumentID]
		if !exists {
			doc, chunks, ok, err := load(source.DocumentID)
			if err != nil {
				return err
			}
			if !ok || doc.WorkspaceID != workspace {
				return fmt.Errorf("%w: document %s is unavailable", ErrSourceUnavailable, source.DocumentID)
			}
			row = record{doc, chunks}
			loaded[source.DocumentID] = row
		}
		if row.document.Version != source.DocumentVersion {
			return fmt.Errorf("%w: document version changed", ErrSourceUnavailable)
		}
		ids := source.SourceChunkIDs
		if len(ids) == 0 {
			ids = []string{source.ChunkID}
		}
		for _, id := range ids {
			found := false
			for _, chunk := range row.chunks {
				if chunk.ID == id && chunk.DocumentID == row.document.ID && predicate.Matches(row.document, chunk) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("%w: chunk %s is unavailable or no longer eligible", ErrSourceUnavailable, id)
			}
		}
	}
	return nil
}
