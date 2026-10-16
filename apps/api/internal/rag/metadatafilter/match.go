// Package metadatafilter owns the legacy exact-text metadata predicate shared
// by fixture recall and context scope checks. PostgreSQL's ->> is the contract.
package metadatafilter

import (
	"bytes"
	"encoding/json"
	"math/big"
	"slices"
	"strconv"
	"strings"
)

// Matches ANDs keys; each key may match either layer, including conflicts.
// Missing/JSON null never match. This is text equality, not typed comparison,
// collection membership, inheritance/override, normalization, or authorization.
func Matches(document, chunk map[string]any, filter map[string]string) bool {
	for key, expected := range filter {
		if !matchesValue(chunk[key], expected) && !matchesValue(document[key], expected) {
			return false
		}
	}
	return true
}

func matchesValue(value any, expected string) bool {
	if value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return text == expected
	}
	data, err := json.Marshal(value)
	if err != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return text == expected
	}
	text, ok := jsonbText(value)
	return ok && text == expected
}

// Arrays/objects retain their literal JSONB text behavior; this adds no new
// filter operator. Keep key ordering, spacing and number scale aligned with SQL.
func jsonbText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		var quoted strings.Builder
		quoted.WriteByte('"')
		for _, char := range typed {
			if char == '"' || char == '\\' || char < 0x20 {
				encoded, _ := json.Marshal(string(char))
				quoted.Write(encoded[1 : len(encoded)-1])
			} else {
				quoted.WriteRune(char)
			}
		}
		quoted.WriteByte('"')
		return quoted.String(), true
	case json.Number:
		mantissa, exponentText, hasExponent := strings.Cut(strings.ToLower(string(typed)), "e")
		exponent := 0
		if hasExponent {
			var err error
			exponent, err = strconv.Atoi(exponentText)
			if err != nil || exponent > 131072 || exponent < -16383 {
				return "", false
			}
		}
		scale := 0
		if index := strings.IndexByte(mantissa, '.'); index >= 0 {
			scale = len(mantissa) - index - 1
		}
		scale = max(0, scale-exponent)
		// Bound allocation to PostgreSQL numeric's supported decimal range.
		if scale > 16383 || len(mantissa) > 131072+16383+2 {
			return "", false
		}
		number, ok := new(big.Rat).SetString(string(typed))
		if !ok {
			return "", false
		}
		return number.FloatString(scale), true
	case []any:
		parts := make([]string, len(typed))
		for index, element := range typed {
			text, ok := jsonbText(element)
			if !ok {
				return "", false
			}
			parts[index] = text
		}
		return "[" + strings.Join(parts, ", ") + "]", true
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		slices.SortFunc(keys, func(a, b string) int {
			if len(a) != len(b) {
				return len(a) - len(b)
			}
			return strings.Compare(a, b)
		})
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			text, ok := jsonbText(typed[key])
			if !ok {
				return "", false
			}
			quoted, _ := jsonbText(key)
			parts = append(parts, quoted+": "+text)
		}
		return "{" + strings.Join(parts, ", ") + "}", true
	default: // The JSON decoder leaves only booleans and null here.
		data, _ := json.Marshal(value)
		return string(data), true
	}
}
