package metadataschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/rag/metadatafilter"
)

var ErrInvalid = failure.New(failure.Definition{Message: "invalid document metadata schema", Info: failure.Info{Code: "invalid_metadata_schema", Source: "knowledge_metadata", Category: failure.CategoryValidation}})

// time.Parse accepts offsets such as +24:00; the public contract is RFC3339.
var dateTimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]([01]\d|2[0-3]):[0-5]\d)$`)

func Invalid(field string) error { return fmt.Errorf("%w: %s", ErrInvalid, field) }

// Empty version is legacy, never an alias for the current contract. Reads do
// not call this validator: old values must remain inspectable and repairable.
func Normalize(version string, metadata map[string]any) (map[string]any, error) {
	if version == "" {
		return metadata, nil
	}
	if version != Version {
		return nil, Invalid("unsupported version")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil || len(encoded) > 65536 {
		return nil, Invalid("metadata exceeds 64 KiB or is not JSON")
	}
	var normalized map[string]any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&normalized); err != nil {
		return nil, Invalid("metadata must be an object")
	}
	if normalized == nil {
		normalized = map[string]any{}
	}
	if len(normalized) > 64 {
		return nil, Invalid("too many fields")
	}
	for key := range normalized {
		if len(key) == 0 || len(key) > 64 {
			return nil, Invalid("field name exceeds 64 bytes or is empty")
		}
	}
	for _, field := range Describe().Fields {
		if field.Owner != "owner_declared" {
			continue
		}
		value, exists := normalized[field.Key]
		if !exists {
			continue
		}
		valid := false
		switch field.Type {
		case "string":
			s, ok := value.(string)
			valid = ok && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= field.MaxLength
		case "enum":
			s, ok := value.(string)
			valid = ok && slices.Contains(field.Values, s)
		case "string-array":
			items, ok := value.([]any)
			valid = ok && len(items) <= field.MaxItems
			for _, item := range items {
				s, ok := item.(string)
				valid = valid && ok && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= field.MaxLength
			}
		case "number":
			if _, ok := value.(json.Number); ok {
				// Bound exponent expansion before exact comparison; float64 can
				// round an out-of-range value back onto an allowed boundary.
				text, supported := metadatafilter.CanonicalJSON(value)
				if supported && len(text) <= 65536 {
					n, ok := new(big.Rat).SetString(text)
					valid = ok && n.Cmp(new(big.Rat).SetFloat64(*field.Minimum)) >= 0 && n.Cmp(new(big.Rat).SetFloat64(*field.Maximum)) <= 0
				}
			}
		case "bool":
			_, valid = value.(bool)
		case "date-time":
			if s, ok := value.(string); ok {
				date, err := time.Parse(time.RFC3339Nano, s)
				valid = err == nil && dateTimePattern.MatchString(s) && date.Equal(date.Truncate(time.Microsecond))
				if valid {
					normalized[field.Key] = date.UTC().Format(time.RFC3339Nano)
				}
			}
		}
		if !valid {
			return nil, Invalid(field.Key + " requires " + field.Type)
		}
	}
	return normalized, nil
}

// A schema upgrade must not relabel historical caller-owned reserved keys as
// generated structure. Reingestion is required to repair those legacy records.
func ValidateDocumentLabels(document domain.Document) error {
	if document.MetadataSchemaVersion == "" {
		return nil
	}
	for key, value := range document.Metadata {
		if !IsSystemField(key) {
			continue
		}
		switch key {
		case "format":
			if value == "text" || value == "markdown" {
				continue
			}
		case "filename":
			if document.SourceType == "file" && value == document.SourceURI {
				continue
			}
		}
		return Invalid(key + " is not a valid Document label; reingest to repair legacy structure")
	}
	return nil
}
