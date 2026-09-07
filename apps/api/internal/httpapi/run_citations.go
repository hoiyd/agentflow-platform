package httpapi

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

func (h *Handler) resolveRunCitations(scoped store.WorkspaceStore, runID, answer string) ([]domain.RAGCitation, []domain.RAGCitation, []string, error) {
	sources, err := h.citationSourcesForRun(scoped, runID)
	if err != nil {
		return nil, nil, nil, err
	}
	citations, invalidSourceIDs := rag.ResolveCitations(answer, sources)
	return sources, citations, invalidSourceIDs, nil
}

func (h *Handler) groundingSourcesForRun(scoped store.WorkspaceStore, runID string) ([]verification.GroundingSource, error) {
	citations, err := h.citationSourcesForRun(scoped, runID)
	if err != nil {
		return nil, err
	}
	documents := map[string][]domain.DocumentChunk{}
	sources := make([]verification.GroundingSource, 0, len(citations))
	for _, citation := range citations {
		chunks, loaded := documents[citation.DocumentID]
		if !loaded {
			document, items, ok, loadErr := scoped.GetDocument(citation.DocumentID)
			if loadErr != nil {
				return nil, fmt.Errorf("load grounding document %s: %w", citation.DocumentID, loadErr)
			}
			if !ok {
				return nil, fmt.Errorf("grounding document %s is unavailable", citation.DocumentID)
			}
			if citation.DocumentVersion != "" && document.Version != citation.DocumentVersion {
				return nil, fmt.Errorf("grounding document %s version changed", citation.DocumentID)
			}
			chunks = items
			documents[citation.DocumentID] = chunks
		}
		wanted := citation.SourceChunkIDs
		if len(wanted) == 0 {
			wanted = []string{citation.ChunkID}
		}
		content := make([]string, 0, len(wanted))
		for _, chunkID := range wanted {
			found := false
			for _, chunk := range chunks {
				if chunk.ID == chunkID {
					content = append(content, strings.TrimSpace(chunk.Content))
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("grounding chunk %s is unavailable", chunkID)
			}
		}
		sources = append(sources, verification.GroundingSource{SourceID: citation.SourceID, Content: strings.Join(content, "\n")})
	}
	return sources, nil
}

func (h *Handler) verificationSubjectForRun(scoped store.WorkspaceStore, run domain.Run, question, output string) verification.Subject {
	if !completionContractUsesVerifier(run.CompletionContract, domain.VerifierGroundedAnswer) {
		return verification.SubjectForQuestionAnswer(question, output)
	}
	sources, err := h.groundingSourcesForRun(scoped, run.ID)
	return verification.SubjectForGroundedQuestionAnswer(question, output, sources, err)
}

func completionContractUsesVerifier(contract *domain.CompletionContract, verifierType domain.VerifierType) bool {
	if contract == nil {
		return false
	}
	for _, spec := range contract.Verifiers {
		if spec.Type == verifierType {
			return true
		}
	}
	return false
}

func (h *Handler) citationSourcesForRun(scoped store.WorkspaceStore, runID string) ([]domain.RAGCitation, error) {
	events, err := scoped.ListRunEvents(runID)
	if err != nil {
		return nil, err
	}
	catalog := map[string]domain.RAGCitation{}
	selectedSourceIDs := []string(nil)
	for _, item := range events {
		switch item.Type {
		case domain.EventRetrievalCompleted:
			var sources []domain.RAGCitation
			if decodePayloadField(item.Payload, "citation_sources", &sources) {
				catalog = make(map[string]domain.RAGCitation, len(sources))
				for _, source := range sources {
					catalog[source.SourceID] = source
				}
			}
		case domain.EventContextAssembled:
			var manifest domain.ContextManifest
			if !decodePayloadField(item.Payload, "manifest", &manifest) {
				continue
			}
			selectedSourceIDs = selectedCitationSourceIDs(manifest)
		}
	}
	sources := make([]domain.RAGCitation, 0, len(selectedSourceIDs))
	for _, sourceID := range selectedSourceIDs {
		if source, ok := catalog[sourceID]; ok {
			sources = append(sources, source)
		}
	}
	return sources, nil
}

func citationSourceIDs(citations []domain.RAGCitation) []string {
	ids := make([]string, 0, len(citations))
	for _, citation := range citations {
		ids = append(ids, citation.SourceID)
	}
	return ids
}

func selectedCitationSourceIDs(manifest domain.ContextManifest) []string {
	ids := make([]string, 0)
	seen := map[string]bool{}
	for _, entry := range manifest.Entries {
		if !entry.Selected || entry.CitationSourceID == "" || seen[entry.CitationSourceID] {
			continue
		}
		ids = append(ids, entry.CitationSourceID)
		seen[entry.CitationSourceID] = true
	}
	return ids
}

func decodePayloadField(payload map[string]any, key string, target any) bool {
	value, ok := payload[key]
	if !ok {
		return false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	return json.Unmarshal(encoded, target) == nil
}
