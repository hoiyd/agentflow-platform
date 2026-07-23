package config

import (
	"testing"
	"time"
)

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

func TestLoadRunBudgetConfiguration(t *testing.T) {
	t.Setenv("RUN_MAX_MODEL_CALLS", "9")
	t.Setenv("RUN_MAX_PROMPT_TOKENS", "1000")
	t.Setenv("RUN_MAX_COMPLETION_TOKENS", "200")
	t.Setenv("RUN_MAX_TOTAL_TOKENS", "1200")
	t.Setenv("RUN_MAX_TOOL_CALLS", "4")
	t.Setenv("RUN_MAX_RUNTIME", "90s")
	t.Setenv("RUN_MAX_ESTIMATED_COST_USD", "0.25")
	t.Setenv("MODEL_INPUT_COST_PER_MILLION_TOKENS_USD", "1.50")
	t.Setenv("MODEL_OUTPUT_COST_PER_MILLION_TOKENS_USD", "6")
	cfg := Load()
	if cfg.RunMaxModelCalls != 9 || cfg.RunMaxPromptTokens != 1000 || cfg.RunMaxCompletionTokens != 200 || cfg.RunMaxTotalTokens != 1200 || cfg.RunMaxToolCalls != 4 || cfg.RunMaxRuntime != 90*time.Second {
		t.Fatalf("unexpected run budget config: %#v", cfg)
	}
	if cfg.RunMaxEstimatedCostMicros != 250_000 || cfg.ModelInputCostPerMillionMicros != 1_500_000 || cfg.ModelOutputCostPerMillionMicros != 6_000_000 {
		t.Fatalf("unexpected cost config: max=%d input=%d output=%d", cfg.RunMaxEstimatedCostMicros, cfg.ModelInputCostPerMillionMicros, cfg.ModelOutputCostPerMillionMicros)
	}
}

func TestLoadRequiresWorkspaceInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("REQUIRE_WORKSPACE_ID", "false")
	t.Setenv("DEFAULT_WORKSPACE_ID", "workspace-local")

	cfg := Load()
	if cfg.Environment != "production" || !cfg.RequireWorkspaceID {
		t.Fatalf("expected production to require workspace IDs, got environment=%q required=%v", cfg.Environment, cfg.RequireWorkspaceID)
	}
	if cfg.DefaultWorkspaceID != "workspace-local" {
		t.Fatalf("unexpected default workspace %q", cfg.DefaultWorkspaceID)
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
