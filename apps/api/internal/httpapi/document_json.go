package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// encoding/json accepts duplicate names with last-value-wins semantics. Reject
// that ambiguity at document write boundaries, including nested extension JSON.
func documentJSONDecoder(reader io.Reader, maxBytes int64) (*json.Decoder, error) {
	return checkedDocumentJSONDecoder(reader, maxBytes, 0)
}

func documentMetadataJSONDecoder(reader io.Reader) (*json.Decoder, error) {
	// The multipart metadata field is nested document data, not a request struct.
	return checkedDocumentJSONDecoder(reader, 65536, 1)
}

func checkedDocumentJSONDecoder(reader io.Reader, maxBytes int64, depth int) (*json.Decoder, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil || int64(len(data)) > maxBytes {
		return nil, errors.New("document JSON exceeds its size limit")
	}
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if err := uniqueDocumentJSON(check, depth); err != nil {
		return nil, err
	}
	if _, err := check.Token(); err != io.EOF {
		return nil, errors.New("trailing document JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder, nil
}

func uniqueDocumentJSON(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("document JSON exceeds 32 nesting levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	if delim != '{' && delim != '[' {
		return errors.New("invalid document JSON")
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delim == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			// Go struct tags also match case-insensitively. Nested free-form
			// metadata remains case-sensitive, as required by the JSON contract.
			if depth == 0 {
				name = strings.ToLower(name)
			}
			if !ok || seen[name] {
				return errors.New("duplicate document JSON key")
			}
			seen[name] = true
		}
		if err := uniqueDocumentJSON(decoder, depth+1); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
