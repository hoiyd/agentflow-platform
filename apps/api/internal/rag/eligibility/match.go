package eligibility

import (
	"encoding/json"
	"math/big"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/metadatafilter"
	"agentflow-platform/apps/api/internal/rag/metadataschema"
)

// Matches never interprets legacy metadata as validated data. Workspace and
// legacy equality remain separate mandatory predicates, outside user OR groups.
func (p Predicate) Matches(document domain.Document, chunk domain.DocumentChunk) bool {
	return lifecycleMatches(document.Lifecycle, p.at) && (p.root == nil || document.MetadataSchemaVersion == metadataschema.Version && p.root.matches(document.Metadata, chunk.Metadata))
}

func (c condition) matches(document, chunk map[string]any) bool {
	if len(c.children) > 0 {
		for _, child := range c.children {
			match := child.matches(document, chunk)
			if c.any && match {
				return true
			}
			if !c.any && !match {
				return false
			}
		}
		return !c.any
	}
	metadata := document
	if c.layer == "chunk" {
		metadata = chunk
	}
	value, ok := metadata[c.key]
	if !ok || value == nil {
		return false
	}
	// Validate only the addressed field. Invalid/corrupt stored values fail
	// closed, without letting a malformed label enter the Context recheck.
	normalized, err := metadataschema.Normalize(metadataschema.Version, map[string]any{c.key: value})
	if err != nil {
		return false
	}
	value = normalized[c.key]
	if c.kind == "string-array" {
		for _, element := range value.([]any) {
			for _, expected := range c.values {
				if element == expected {
					return true
				}
			}
		}
		return false
	}
	for _, expected := range c.values {
		comparison := 0
		switch c.kind {
		case "number":
			left, _ := metadatafilter.CanonicalJSON(value)
			right, _ := metadatafilter.CanonicalJSON(expected)
			a, _ := new(big.Rat).SetString(left)
			b, _ := new(big.Rat).SetString(right)
			comparison = a.Cmp(b)
		case "date-time":
			a, err := time.Parse(time.RFC3339Nano, value.(string))
			if err != nil || a.Year() < 1 || a.Year() > 9999 {
				return false
			}
			b, _ := time.Parse(time.RFC3339Nano, expected.(string))
			comparison = a.Compare(b)
		default:
			a, _ := json.Marshal(value)
			b, _ := json.Marshal(expected)
			if string(a) != string(b) {
				comparison = 1
			}
		}
		if (c.op == "eq" || c.op == "in") && comparison == 0 || c.op == "gt" && comparison > 0 || c.op == "gte" && comparison >= 0 || c.op == "lt" && comparison < 0 || c.op == "lte" && comparison <= 0 {
			return true
		}
	}
	return false
}
