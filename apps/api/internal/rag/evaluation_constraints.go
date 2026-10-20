package rag

import (
	"fmt"
	"maps"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/redaction"
)

// EvaluationSearch is shared by HTTP evaluation and the offline runner. Case
// conditions narrow the request; they never replace its Workspace or metadata.
func EvaluationSearch(request domain.RAGEvaluationRunRequest, c domain.RAGEvaluationCase) (domain.DocumentSearch, error) {
	search := domain.DocumentSearch{Query: c.Query, WorkspaceID: request.WorkspaceID,
		Metadata: maps.Clone(request.Metadata), MetadataFilter: request.MetadataFilter,
		Limit: NormalizeSearchLimit(request.TopK), MinSimilarity: request.MinSimilarity}
	if len(c.Metadata) > 0 && search.Metadata == nil {
		search.Metadata = map[string]string{}
	}
	for key, value := range c.Metadata {
		if existing, ok := search.Metadata[key]; ok && existing != value {
			return search, fmt.Errorf("case %q: metadata %q conflicts with request scope", c.ID, key)
		}
		search.Metadata[key] = value
	}
	if c.MetadataFilter != nil {
		search.MetadataFilter = c.MetadataFilter
		if request.MetadataFilter != nil {
			if request.MetadataFilter.SchemaVersion != c.MetadataFilter.SchemaVersion {
				return search, fmt.Errorf("%w: case %q schema differs from request", eligibility.ErrInvalid, c.ID)
			}
			search.MetadataFilter = &domain.MetadataFilter{SchemaVersion: c.MetadataFilter.SchemaVersion,
				Where: domain.MetadataCondition{All: []domain.MetadataCondition{request.MetadataFilter.Where, c.MetadataFilter.Where}}}
		}
	}
	if c.RequireFilter && len(search.Metadata) == 0 && search.MetadataFilter == nil {
		return search, fmt.Errorf("case %q requires a nonempty filter", c.ID)
	}
	if _, err := eligibility.Compile(search.MetadataFilter); err != nil {
		return search, fmt.Errorf("case %q: %w", c.ID, err)
	}
	if err := redaction.ValidateValue(search.Metadata); err != nil {
		return search, err
	}
	if err := redaction.ValidateValue(search.MetadataFilter); err != nil {
		return search, err
	}
	return search, nil
}
