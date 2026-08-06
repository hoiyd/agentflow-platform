package rag

import (
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func ResolveEvaluationCases(request domain.RAGEvaluationRunRequest) ([]domain.RAGEvaluationCase, *domain.RAGGoldenDatasetInfo, error) {
	if request.Dataset != nil && len(request.Cases) > 0 {
		return nil, nil, errors.New("provide either dataset or cases, not both")
	}
	if request.Dataset == nil {
		if len(request.Cases) == 0 {
			return nil, nil, errors.New("at least one evaluation case is required")
		}
		if len(request.Cases) > 50 {
			return nil, nil, errors.New("at most 50 evaluation cases are supported")
		}
		return request.Cases, nil, nil
	}

	dataset := request.Dataset
	if strings.TrimSpace(dataset.SchemaVersion) != domain.RAGGoldenDatasetSchemaVersion {
		return nil, nil, fmt.Errorf("dataset schema_version must be %q", domain.RAGGoldenDatasetSchemaVersion)
	}
	if strings.TrimSpace(dataset.ID) == "" {
		return nil, nil, errors.New("dataset id is required")
	}
	if strings.TrimSpace(dataset.Version) == "" {
		return nil, nil, errors.New("dataset version is required")
	}
	if len(dataset.Cases) == 0 {
		return nil, nil, errors.New("dataset requires at least one case")
	}
	if len(dataset.Cases) > 50 {
		return nil, nil, errors.New("dataset supports at most 50 cases per evaluation run")
	}
	if err := validateTags("dataset", dataset.Tags); err != nil {
		return nil, nil, err
	}

	seenCaseIDs := make(map[string]struct{}, len(dataset.Cases))
	for index, evalCase := range dataset.Cases {
		caseID := strings.TrimSpace(evalCase.ID)
		if caseID == "" {
			return nil, nil, fmt.Errorf("dataset case %d requires id", index)
		}
		if _, duplicate := seenCaseIDs[caseID]; duplicate {
			return nil, nil, fmt.Errorf("dataset case id %q is duplicated", caseID)
		}
		seenCaseIDs[caseID] = struct{}{}
		if strings.TrimSpace(evalCase.Query) == "" {
			return nil, nil, fmt.Errorf("dataset case %q requires query", caseID)
		}
		if evalCase.Answerable == nil {
			return nil, nil, fmt.Errorf("dataset case %q requires answerable", caseID)
		}
		if hasLegacyExpectations(evalCase) {
			return nil, nil, fmt.Errorf("dataset case %q must use expected_sources instead of legacy expected fields", caseID)
		}
		if *evalCase.Answerable && len(evalCase.ExpectedSources) == 0 {
			return nil, nil, fmt.Errorf("answerable dataset case %q requires expected_sources", caseID)
		}
		if !*evalCase.Answerable && len(evalCase.ExpectedSources) > 0 {
			return nil, nil, fmt.Errorf("unanswerable dataset case %q cannot define expected_sources", caseID)
		}
		if !*evalCase.Answerable && evalCase.RequiredSourceCount > 0 {
			return nil, nil, fmt.Errorf("unanswerable dataset case %q cannot define required_source_count", caseID)
		}
		if evalCase.RequiredSourceCount < 0 {
			return nil, nil, fmt.Errorf("dataset case %q required_source_count must be positive", caseID)
		}
		if evalCase.RequiredSourceCount > len(evalCase.ExpectedSources) {
			return nil, nil, fmt.Errorf("dataset case %q required_source_count cannot exceed expected_sources", caseID)
		}
		if err := validateGoldenSources(caseID, "expected_sources", evalCase.ExpectedSources); err != nil {
			return nil, nil, err
		}
		if err := validateGoldenSources(caseID, "forbidden_sources", evalCase.ForbiddenSources); err != nil {
			return nil, nil, err
		}
		if err := validateTags("dataset case "+caseID, evalCase.Tags); err != nil {
			return nil, nil, err
		}
	}

	info := &domain.RAGGoldenDatasetInfo{
		SchemaVersion: strings.TrimSpace(dataset.SchemaVersion),
		ID:            strings.TrimSpace(dataset.ID),
		Version:       strings.TrimSpace(dataset.Version),
		Description:   strings.TrimSpace(dataset.Description),
		Tags:          append([]string(nil), dataset.Tags...),
	}
	return dataset.Cases, info, nil
}

func validateGoldenSources(caseID string, field string, sources []domain.RAGGoldenSource) error {
	for index, source := range sources {
		if strings.TrimSpace(source.DocumentID) == "" && strings.TrimSpace(source.ChunkID) == "" && strings.TrimSpace(source.SourceURI) == "" && !hasNonEmptyString(source.ContentContains) {
			return fmt.Errorf("dataset case %q %s[%d] requires document_id, chunk_id, source_uri, or content_contains", caseID, field, index)
		}
		if !allStringsNonEmpty(source.ContentContains) {
			return fmt.Errorf("dataset case %q %s[%d] contains an empty content_contains value", caseID, field, index)
		}
	}
	return nil
}

func validateTags(owner string, tags []string) error {
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return fmt.Errorf("%s contains an empty tag", owner)
		}
		if _, duplicate := seen[tag]; duplicate {
			return fmt.Errorf("%s contains duplicate tag %q", owner, tag)
		}
		seen[tag] = struct{}{}
	}
	return nil
}

func hasLegacyExpectations(evalCase domain.RAGEvaluationCase) bool {
	return len(evalCase.ExpectedDocumentIDs) > 0 || len(evalCase.ExpectedChunkIDs) > 0 || len(evalCase.ExpectedChunkContains) > 0
}

func hasNonEmptyString(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func allStringsNonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}
