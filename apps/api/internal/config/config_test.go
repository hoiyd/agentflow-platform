package config

import "testing"

func TestNormalizeAdaptiveMemoryMode(t *testing.T) {
	tests := map[string]string{
		"shadow":  "shadow",
		" AUTO ":  "auto",
		"off":     "off",
		"unknown": "off",
	}
	for input, want := range tests {
		if got := normalizeAdaptiveMemoryMode(input); got != want {
			t.Fatalf("normalize mode %q: got %q want %q", input, got, want)
		}
	}
}

func TestGetUnitFloatEnvRejectsOutOfRangeValue(t *testing.T) {
	t.Setenv("TEST_UNIT_FLOAT", "0.91")
	if got := getUnitFloatEnv("TEST_UNIT_FLOAT", 0.85); got != 0.91 {
		t.Fatalf("unit float: got %v", got)
	}
	t.Setenv("TEST_UNIT_FLOAT", "1.2")
	if got := getUnitFloatEnv("TEST_UNIT_FLOAT", 0.85); got != 0.85 {
		t.Fatalf("out-of-range unit float: got %v", got)
	}
}
