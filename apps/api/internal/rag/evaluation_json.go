package rag

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"agentflow-platform/apps/api/internal/rag/eligibility"
)

// DecodeEvaluationJSON is used by both dataset files and HTTP requests. Unknown
// or ambiguous restrictions are errors, never a fallback to an unfiltered case.
func DecodeEvaluationJSON(data []byte, target any) error {
	if len(data) > 2<<20 {
		return errors.New("evaluation JSON exceeds 2 MiB")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := checkEvaluationJSON(check, 0, false); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return errors.New("trailing evaluation JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func checkEvaluationJSON(decoder *json.Decoder, depth int, metadata bool) error {
	if depth > 32 {
		return errors.New("evaluation JSON exceeds 32 nesting levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		key := ""
		if delim == '{' {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key = token.(string)
			name := key
			if !metadata {
				name = strings.ToLower(key)
			}
			if seen[name] {
				return errors.New("duplicate evaluation JSON key")
			}
			seen[name] = true
			if !metadata && (name == "metadata_filter" || name == "metadata" || name == "require_filter") {
				if key != name {
					return errors.New("evaluation constraints require canonical field names")
				}
				var raw json.RawMessage
				if err := decoder.Decode(&raw); err != nil {
					return err
				}
				if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
					return errors.New("evaluation constraint must not be null")
				}
				if name == "metadata_filter" {
					if err := eligibility.ValidateFilterJSON(raw); err != nil {
						return err
					}
				}
				if name == "metadata" {
					var labels map[string]json.RawMessage
					if json.Unmarshal(raw, &labels) != nil {
						return errors.New("evaluation metadata must be an object")
					}
					for _, value := range labels {
						var text string
						if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
							return errors.New("evaluation metadata values must be strings")
						}
					}
				}
				child := json.NewDecoder(bytes.NewReader(raw))
				child.UseNumber()
				if err := checkEvaluationJSON(child, depth+1, name == "metadata"); err != nil {
					return err
				}
				continue
			}
		}
		if err := checkEvaluationJSON(decoder, depth+1, metadata); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
