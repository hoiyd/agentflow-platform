package eligibility

import (
	"encoding/json"
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/rag/metadataschema"
)

var ErrLifecycle = failure.New(failure.Definition{
	Message: "invalid document lifecycle", Info: failure.Info{Code: "invalid_document_lifecycle", Source: "knowledge", Category: failure.CategoryValidation},
})

// DecodeLifecycle preserves absence versus explicit null at both HTTP and
// persistence boundaries; encoding/json otherwise turns null strings into "".
func DecodeLifecycle(data []byte) (domain.DocumentLifecycle, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return domain.DocumentLifecycle{}, fmt.Errorf("%w: lifecycle must be an object", ErrLifecycle)
	}
	var value domain.DocumentLifecycle
	for key, raw := range fields {
		var target *string
		switch key {
		case "status":
			target = &value.Status
		case "valid_from":
			target = &value.ValidFrom
		case "valid_until":
			target = &value.ValidUntil
		case "published_at":
			target = &value.PublishedAt
		default:
			return domain.DocumentLifecycle{}, fmt.Errorf("%w: unknown field %s", ErrLifecycle, key)
		}
		if json.Unmarshal(raw, target) != nil || *target == "" {
			return domain.DocumentLifecycle{}, fmt.Errorf("%w: %s must be a nonempty string", ErrLifecycle, key)
		}
	}
	return NormalizeLifecycle(value)
}

// NormalizeLifecycle deliberately does not read metadata: legacy labels are not
// trusted lifecycle declarations. Reuse the metadata contract's strict RFC3339
// validator, then keep only canonical UTC microsecond-precision timestamps.
func NormalizeLifecycle(input domain.DocumentLifecycle) (domain.DocumentLifecycle, error) {
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Status != "active" && input.Status != "draft" && input.Status != "withdrawn" {
		return domain.DocumentLifecycle{}, fmt.Errorf("%w: status must be active, draft or withdrawn", ErrLifecycle)
	}
	for name, value := range map[string]*string{"valid_from": &input.ValidFrom, "valid_until": &input.ValidUntil, "published_at": &input.PublishedAt} {
		if *value == "" {
			continue
		}
		normalized, err := metadataschema.Normalize(metadataschema.Version, map[string]any{"published_at": *value})
		if err != nil {
			return domain.DocumentLifecycle{}, fmt.Errorf("%w: %s requires RFC3339 with timezone and microsecond precision", ErrLifecycle, name)
		}
		*value = normalized["published_at"].(string)
		parsed, err := time.Parse(time.RFC3339Nano, *value)
		if err != nil || parsed.Year() < 1 || parsed.Year() > 9999 {
			return domain.DocumentLifecycle{}, fmt.Errorf("%w: %s UTC year must be 0001..9999", ErrLifecycle, name)
		}
	}
	if input.ValidFrom != "" && input.ValidUntil != "" {
		from, _ := time.Parse(time.RFC3339Nano, input.ValidFrom)
		until, _ := time.Parse(time.RFC3339Nano, input.ValidUntil)
		if !from.Before(until) {
			return domain.DocumentLifecycle{}, fmt.Errorf("%w: valid_from must precede valid_until", ErrLifecycle)
		}
	}
	return input, nil
}

func lifecycleMatches(input domain.DocumentLifecycle, at time.Time) bool {
	value, err := NormalizeLifecycle(input)
	if err != nil || value.Status != "active" {
		return false
	}
	if value.ValidFrom != "" {
		from, _ := time.Parse(time.RFC3339Nano, value.ValidFrom)
		if at.Before(from) {
			return false
		}
	}
	if value.ValidUntil != "" {
		until, _ := time.Parse(time.RFC3339Nano, value.ValidUntil)
		if !at.Before(until) {
			return false
		}
	}
	return true
}

func (p Predicate) lifecycleSQL(args *[]any) string {
	at := bind(args, p.at)
	// Writes validate/normalize JSON; guard casts anyway so corrupt stored data
	// fails closed instead of failing the entire query. Missing legacy fields
	// mean active/unbounded, not a synthesized time derived from created_at.
	date := func(key, operator string) string {
		value := "(d.lifecycle ->> '" + key + "')"
		return "(CASE WHEN NOT (d.lifecycle ? '" + key + "') THEN TRUE WHEN jsonb_typeof(d.lifecycle -> '" + key + "') = 'string' AND " + value + ` ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?Z$' AND pg_input_is_valid(` + value + ", 'timestamp with time zone') THEN " + value + "::timestamptz " + operator + " " + at + "::timestamptz ELSE FALSE END)"
	}
	return "(jsonb_typeof(d.lifecycle) = 'object' AND (NOT (d.lifecycle ? 'status') OR d.lifecycle ->> 'status' = 'active') AND " + date("valid_from", "<=") + " AND " + date("valid_until", ">") + ")"
}
