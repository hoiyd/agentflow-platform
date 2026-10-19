package eligibility

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/metadatafilter"
)

const ScopePolicyVersion = "knowledge-filter-v1"

func FreezeScope(filter *domain.MetadataFilter) (*domain.KnowledgeScope, error) {
	if filter == nil {
		return nil, nil
	}
	predicate, err := Compile(filter)
	if err != nil {
		return nil, err
	}
	normalized := domain.MetadataFilter{SchemaVersion: filter.SchemaVersion, Where: predicate.root.normalized()}
	data, err := json.Marshal(normalized)
	if err != nil || len(data) > MaxBytes {
		return nil, invalid("normalized filter exceeds its size limit")
	}
	return &domain.KnowledgeScope{Filter: normalized, ID: fmt.Sprintf("sha256:%x", sha256.Sum256(data)), PolicyVersion: ScopePolicyVersion, Source: "run_request"}, nil
}

func (c condition) normalized() domain.MetadataCondition {
	result := domain.MetadataCondition{Field: c.layer + ".metadata." + c.key, Op: c.op}
	if len(c.children) > 0 {
		result = domain.MetadataCondition{}
		children := make([]domain.MetadataCondition, 0, len(c.children))
		for _, child := range c.children {
			children = append(children, child.normalized())
		}
		if c.any {
			result.Any = children
		} else {
			result.All = children
		}
		return result
	}
	values := append([]any(nil), c.values...)
	if c.kind == "number" {
		for i, value := range values {
			text, _ := metadatafilter.CanonicalJSON(value)
			if strings.Contains(text, ".") {
				text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
			}
			values[i] = json.Number(text)
		}
	}
	result.Value = values[0]
	if c.op == "in" {
		result.Value = values
	}
	return result
}

// FrozenFilter validates persisted identity every time it crosses a read/Resume
// boundary. Unsupported/corrupt scopes cannot degrade to no filter.
func FrozenFilter(snapshot *domain.RuntimeSnapshot) (*domain.MetadataFilter, error) {
	if snapshot == nil || snapshot.KnowledgeScope == nil {
		return nil, nil
	}
	scope := snapshot.KnowledgeScope
	canonical, err := FreezeScope(&scope.Filter)
	if err != nil {
		return nil, err
	}
	if scope.Source != "run_request" || scope.PolicyVersion != ScopePolicyVersion || scope.ID != canonical.ID {
		return nil, invalid("frozen Knowledge scope identity is invalid")
	}
	return &canonical.Filter, nil
}
