package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCalculatorTool(t *testing.T) {
	registry := DefaultRegistry()
	args := json.RawMessage(`{"expression":"128 * 37 + (10 / 2)"}`)

	result := registry.Execute(context.Background(), "calculator", args)
	if result.Error != "" {
		t.Fatalf("unexpected tool error: %s", result.Error)
	}

	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", result.Result)
	}
	if payload["value"] != float64(4741) {
		t.Fatalf("expected 4741, got %v", payload["value"])
	}
}

func TestCalculatorRejectsDivisionByZero(t *testing.T) {
	_, err := evalExpression("10 / (5 - 5)")
	if err == nil {
		t.Fatal("expected division by zero error")
	}
}
