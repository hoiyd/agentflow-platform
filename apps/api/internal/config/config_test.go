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

func TestLoadChildRunDelegationConfiguration(t *testing.T) {
	t.Setenv("MAX_CONCURRENT_CHILD_RUNS", "3")
	t.Setenv("MAX_CHILD_RUNS_PER_PARENT", "2")
	t.Setenv("CHILD_RUN_TIMEOUT", "75s")
	t.Setenv("CHILD_RUN_SUMMARY_MAX_CHARACTERS", "900")
	t.Setenv("CHILD_RUN_MAX_MODEL_CALLS", "4")
	t.Setenv("CHILD_RUN_MAX_TOTAL_TOKENS", "6000")
	t.Setenv("CHILD_RUN_MAX_TOOL_CALLS", "3")
	cfg := Load()
	if cfg.MaxConcurrentChildRuns != 3 || cfg.MaxChildRunsPerParent != 2 || cfg.ChildRunTimeout != 75*time.Second ||
		cfg.ChildRunSummaryMaxCharacters != 900 || cfg.ChildRunMaxModelCalls != 4 ||
		cfg.ChildRunMaxTotalTokens != 6000 || cfg.ChildRunMaxToolCalls != 3 {
		t.Fatalf("unexpected child run config: %#v", cfg)
	}
}

func TestLoadSessionHistoryRetrievalConfiguration(t *testing.T) {
	t.Setenv("CONTEXT_HISTORY_RETRIEVAL_MAX_RESULTS", "6")
	t.Setenv("CONTEXT_HISTORY_RETRIEVAL_MAX_CHARACTERS", "9000")
	t.Setenv("CONTEXT_HISTORY_RETRIEVAL_MAX_TOKENS", "2200")
	t.Setenv("CONTEXT_HISTORY_RETRIEVAL_WINDOW", "2")
	cfg := Load()
	if cfg.ContextHistoryRetrievalMaxResults != 6 || cfg.ContextHistoryRetrievalMaxChars != 9000 ||
		cfg.ContextHistoryRetrievalMaxTokens != 2200 || cfg.ContextHistoryRetrievalWindow != 2 {
		t.Fatalf("unexpected session history retrieval config: %#v", cfg)
	}
}

func TestLoadModelRequestCaptureConfiguration(t *testing.T) {
	t.Setenv("MODEL_REQUEST_CAPTURE_MODE", " REDACTED ")
	t.Setenv("MODEL_REQUEST_CAPTURE_MAX_BYTES", "8192")
	t.Setenv("MODEL_REQUEST_CAPTURE_RETENTION", "48h")
	cfg := Load()
	if cfg.ModelRequestCaptureMode != "redacted" || cfg.ModelRequestCaptureMaxBytes != 8192 || cfg.ModelRequestCaptureRetention != 48*time.Hour {
		t.Fatalf("unexpected model request capture config: %#v", cfg)
	}
	if got := normalizeModelRequestCaptureMode("unsafe"); got != "metadata_only" {
		t.Fatalf("unknown capture mode should fail safe, got %q", got)
	}
}

func TestLoadToolProgressGuardConfiguration(t *testing.T) {
	t.Setenv("TOOL_PROGRESS_GUARD_ENABLED", "false")
	t.Setenv("TOOL_PROGRESS_WARN_AFTER", "3")
	t.Setenv("TOOL_PROGRESS_BLOCK_AFTER", "5")
	t.Setenv("TOOL_PROGRESS_HALT_AFTER", "7")
	cfg := Load()
	if cfg.ToolProgressGuardEnabled || cfg.ToolProgressWarnAfter != 3 || cfg.ToolProgressBlockAfter != 5 || cfg.ToolProgressHaltAfter != 7 {
		t.Fatalf("unexpected Tool Progress Guard config: %#v", cfg)
	}
	t.Setenv("TOOL_PROGRESS_GUARD_ENABLED", "not-a-bool")
	if !getBoolEnv("TOOL_PROGRESS_GUARD_ENABLED", true) {
		t.Fatal("invalid bool did not use fallback")
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
