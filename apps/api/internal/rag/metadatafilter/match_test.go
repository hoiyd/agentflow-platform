package metadatafilter

import (
	"encoding/json"
	"math"
	"testing"
)

// Invalid Go/JSON values cannot broaden a filter. These inputs cannot be
// persisted as JSONB; a valid value on the other layer may still satisfy OR.
func TestMatchesRejectsUnrepresentableValues(t *testing.T) {
	var pointer *string
	for _, value := range []any{pointer, math.NaN(), math.Inf(1), func() {}, json.RawMessage(`{`), json.Number("not-a-number"), json.Number("1e999999"), json.Number("1e-999999")} {
		if Matches(map[string]any{"key": value}, nil, map[string]string{"key": ""}) {
			t.Errorf("unrepresentable value %T matched an empty string", value)
		}
		if !Matches(map[string]any{"key": value}, map[string]any{"key": "valid"}, map[string]string{"key": "valid"}) {
			t.Errorf("invalid Document value %T shadowed the valid Chunk", value)
		}
	}
}

func TestMatchesJSONNumberNotation(t *testing.T) {
	for input, expected := range map[string]string{"1.20e2": "120", "1.20e-2": "0.0120", "-0.00": "0.00", "-1.25": "-1.25"} {
		if !Matches(map[string]any{"key": json.Number(input)}, nil, map[string]string{"key": expected}) {
			t.Errorf("%s did not match %q", input, expected)
		}
	}
}
