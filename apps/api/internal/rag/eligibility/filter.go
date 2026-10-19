// Package eligibility compiles the bounded metadata contract once per Store
// operation into equivalent in-memory and parameterized SQL predicates.
package eligibility

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/rag/metadataschema"
)

var ErrInvalid = failure.New(failure.Definition{Message: "invalid typed metadata filter", Info: failure.Info{Code: "invalid_metadata_filter", Source: "knowledge_metadata", Category: failure.CategoryValidation}})

var ErrInvariant = failure.New(failure.Definition{Message: "retrieval candidate violated eligibility", Info: failure.Info{Code: "knowledge_eligibility_violation", Source: "knowledge_index", Category: failure.CategoryInternal}})

const (
	MaxDepth   = 4
	MaxNodes   = 32
	MaxSetSize = 32
	MaxBytes   = 16384
)

type Predicate struct {
	root *condition
	at   time.Time
}
type condition struct {
	children             []condition
	any                  bool
	layer, key, kind, op string
	values               []any
}

// Compile rejects invalid input even for direct Store consumers. A nil filter
// alone means no typed restriction; an empty supplied filter is never ignored.
func Compile(filter *domain.MetadataFilter) (Predicate, error) {
	return CompileAt(filter, time.Now().UTC())
}

// CompileAt fixes one operation's eligibility instant. Only internal callers
// can supply it; request JSON cannot override the server clock.
func CompileAt(filter *domain.MetadataFilter, at time.Time) (Predicate, error) {
	if filter == nil {
		return Predicate{at: at}, nil
	}
	data, err := json.Marshal(filter)
	if err != nil || len(data) > MaxBytes {
		return Predicate{}, invalid("expression exceeds 16 KiB or is not JSON")
	}
	if filter.SchemaVersion != metadataschema.Version {
		return Predicate{}, invalid("unsupported schema_version")
	}
	nodes := 0
	root, err := compileCondition(filter.Where, 1, &nodes)
	if err != nil {
		return Predicate{}, err
	}
	return Predicate{root: &root, at: at}, nil
}

func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalid, reason) }

func compileCondition(input domain.MetadataCondition, depth int, nodes *int) (condition, error) {
	*nodes = *nodes + 1
	if depth > MaxDepth || *nodes > MaxNodes {
		return condition{}, invalid("expression depth or node limit exceeded")
	}
	groups := 0
	if input.All != nil {
		groups++
	}
	if input.Any != nil {
		groups++
	}
	if groups > 0 {
		if groups != 1 || input.Field != "" || input.Op != "" || input.Value != nil {
			return condition{}, invalid("group cannot contain a leaf or another group operator")
		}
		children := input.All
		if input.Any != nil {
			children = input.Any
		}
		if len(children) == 0 {
			return condition{}, invalid("group must not be empty")
		}
		result := condition{any: input.Any != nil}
		for _, child := range children {
			compiled, err := compileCondition(child, depth+1, nodes)
			if err != nil {
				return condition{}, err
			}
			result.children = append(result.children, compiled)
		}
		return result, nil
	}
	layer, key, ok := strings.Cut(input.Field, ".metadata.")
	if !ok || layer != "document" && layer != "chunk" {
		return condition{}, invalid("field requires an explicit document.metadata or chunk.metadata address")
	}
	var field domain.DocumentMetadataField
	for _, candidate := range metadataschema.Describe().Fields {
		if candidate.Key == key && candidate.Owner == "owner_declared" {
			field = candidate
			break
		}
	}
	if field.Key == "" {
		return condition{}, invalid("field is not registered for typed filtering")
	}
	if field.Scope != "both" && field.Scope != layer {
		return condition{}, invalid("field is not available on this layer")
	}
	switch input.Op {
	case "eq":
		if field.Type == "string-array" {
			return condition{}, invalid("arrays support contains or in, not eq")
		}
	case "in":
	case "contains":
		if field.Type != "string-array" {
			return condition{}, invalid("contains requires an array field")
		}
	case "lt", "lte", "gt", "gte":
		if field.Type != "number" && field.Type != "date-time" {
			return condition{}, invalid("range requires a number or date-time field")
		}
	default:
		return condition{}, invalid("unsupported operator")
	}
	// JSON round-trip detaches caller-owned arrays and preserves exact numbers.
	data, err := json.Marshal(input.Value)
	if err != nil {
		return condition{}, invalid("value is not JSON")
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || value == nil {
		return condition{}, invalid("value must not be null or omitted")
	}
	values := []any{value}
	if input.Op == "in" {
		var ok bool
		values, ok = value.([]any)
		if !ok || len(values) == 0 || len(values) > MaxSetSize {
			return condition{}, invalid("in requires 1 to 32 values")
		}
	}
	for i, v := range values {
		check := v
		if field.Type == "string-array" {
			check = []any{v}
		}
		normalized, err := metadataschema.Normalize(metadataschema.Version, map[string]any{key: check})
		if err != nil {
			return condition{}, invalid("value does not match the registered field type or bounds")
		}
		values[i] = normalized[key]
		if field.Type == "date-time" {
			date, err := time.Parse(time.RFC3339Nano, values[i].(string))
			if err != nil || date.Year() < 1 || date.Year() > 9999 {
				return condition{}, invalid("date-time requires a UTC year between 0001 and 9999")
			}
		}
		if field.Type == "string-array" {
			values[i] = normalized[key].([]any)[0]
		}
	}
	return condition{layer: layer, key: key, kind: field.Type, op: input.Op, values: values}, nil
}
