package eligibility

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/rag/metadataschema"
)

// SQL appends bind values to the caller's argument list. Only validated fixed
// column/operator tokens become SQL; schema, keys and values are parameters.
// The caller ANDs this with Workspace, legacy filters and source identity.
func (p Predicate) SQL(args *[]any) string {
	if p.root == nil {
		return "TRUE"
	}
	version := bind(args, metadataschema.Version)
	return "(d.metadata_schema_version = " + version + " AND " + p.root.sql(args) + ")"
}

func bind(args *[]any, value any) string {
	*args = append(*args, value)
	return fmt.Sprintf("$%d", len(*args))
}

func (c condition) sql(args *[]any) string {
	if len(c.children) > 0 {
		parts := make([]string, 0, len(c.children))
		for _, child := range c.children {
			parts = append(parts, child.sql(args))
		}
		join := " AND "
		if c.any {
			join = " OR "
		}
		return "(" + strings.Join(parts, join) + ")"
	}
	column := "d.metadata"
	if c.layer == "chunk" {
		column = "c.metadata"
	}
	key := bind(args, c.key)
	value := "(" + column + " -> " + key + "::text)"
	text := "(" + column + " ->> " + key + "::text)"
	operator := map[string]string{"eq": "=", "in": "=", "contains": "=", "lt": "<", "lte": "<=", "gt": ">", "gte": ">="}[c.op]
	parts := make([]string, 0, len(c.values))
	for _, expected := range c.values {
		encoded, _ := json.Marshal(expected)
		parameter := bind(args, string(encoded))
		switch c.kind {
		case "number":
			// CASE guards casts independently of planner predicate reordering.
			parts = append(parts, "(CASE WHEN jsonb_typeof("+value+") = 'number' THEN ("+text+")::numeric "+operator+" ("+parameter+"::jsonb #>> '{}')::numeric ELSE FALSE END)")
		case "date-time":
			// Versioned writes normalize dates to UTC. Restrict the supported
			// representation before PostgreSQL's more permissive timestamp parser.
			canonical := text + ` ~ '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?Z$'`
			parts = append(parts, "(CASE WHEN jsonb_typeof("+value+") = 'string' AND "+canonical+" AND pg_input_is_valid("+text+", 'timestamp with time zone') THEN ("+text+")::timestamptz "+operator+" ("+parameter+"::jsonb #>> '{}')::timestamptz ELSE FALSE END)")
		case "string-array":
			parts = append(parts, "(jsonb_typeof("+value+") = 'array' AND "+value+" @> jsonb_build_array("+parameter+"::jsonb))")
		default:
			parts = append(parts, "("+value+" = "+parameter+"::jsonb)")
		}
	}
	return "COALESCE((" + strings.Join(parts, " OR ") + "), FALSE)"
}
