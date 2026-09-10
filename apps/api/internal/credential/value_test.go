package credential

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestEnvironmentValueDoesNotFormatOrSerializeSecret(t *testing.T) {
	t.Setenv("TEST_PROVIDER_KEY", "sk-private-value")
	value := FromEnvironment("TEST_PROVIDER_KEY")
	if !value.Available() || value.Reveal() != "sk-private-value" {
		t.Fatal("credential was not resolved at runtime")
	}
	if got := fmt.Sprintf("%v %#v", value, value); got != "[REDACTED] [REDACTED]" {
		t.Fatalf("credential formatting leaked or changed: %q", got)
	}
	if _, err := json.Marshal(value); err == nil {
		t.Fatal("credential value was serializable")
	}
}
