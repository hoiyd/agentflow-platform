package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestToolSnapshotWireContractRoundTrip(t *testing.T) {
	const fixture = `{
		"schema_version":18,
		"tool_security_policy":{"version":"tool-security-policy-v1","default_action":"deny","rules":[{"id":"review","tool":"send_report","action":"ask","capability":{"source":"local","scope":{"resources":[{"kind":"workspace","name":"reports","access":"write"}],"network":{"mode":"external","targets":["reports.example"]},"credential_scopes":["reports"]},"side_effect_class":"external_write","rate":"elevated","reversibility":"compensatable","visibility":"operator","approval_mode":"ask","audit_level":"full"}}]},
		"tool_progress_guard":{"version":"tool-progress-guard-v1","enabled":true,"warn_after":2,"block_after":4,"halt_after":5,"history_max":8},
		"tools":[{"name":"send_report","description":"Send a report","parameters":{"type":"object"},"schema_version":"v1","definition_revision":"rev1","side_effect":"external","security":{"source":"local","scope":{"resources":[{"kind":"workspace","name":"reports","access":"write"}],"network":{"mode":"external","targets":["reports.example"]},"credential_scopes":["reports"]},"side_effect_class":"external_write","rate":"elevated","reversibility":"compensatable","visibility":"operator","approval_mode":"ask","audit_level":"full"}}]
	}`
	var snapshot RuntimeSnapshot
	if err := json.Unmarshal([]byte(fixture), &snapshot); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if err := json.Unmarshal([]byte(fixture), &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "tool_security_policy", "tool_progress_guard", "tools"} {
		if !reflect.DeepEqual(before[key], after[key]) {
			t.Fatalf("snapshot %s changed on round trip: before=%#v after=%#v", key, before[key], after[key])
		}
	}
}
