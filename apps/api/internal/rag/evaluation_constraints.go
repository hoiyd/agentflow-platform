package rag

import (
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"agentflow-platform/apps/api/internal/redaction"
)

// EvaluationSearch is shared by HTTP evaluation and the offline runner. Case
// conditions narrow the request; they never replace its Workspace or filter.
func EvaluationSearch(request domain.RAGEvaluationRunRequest, c domain.RAGEvaluationCase) (domain.DocumentSearch, error) {
	search := domain.DocumentSearch{Query: c.Query, WorkspaceID: request.WorkspaceID,
		MetadataFilter: request.MetadataFilter,
		Limit:          NormalizeSearchLimit(request.TopK), MinSimilarity: request.MinSimilarity}
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
	if c.RequireFilter && search.MetadataFilter == nil {
		return search, fmt.Errorf("case %q requires a nonempty filter", c.ID)
	}
	if _, err := eligibility.Compile(search.MetadataFilter); err != nil {
		return search, fmt.Errorf("case %q: %w", c.ID, err)
	}
	if err := redaction.ValidateValue(search.MetadataFilter); err != nil {
		return search, err
	}
	return search, nil
}
