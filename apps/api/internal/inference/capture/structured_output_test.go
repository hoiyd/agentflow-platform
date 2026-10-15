package capture

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOutputSchemaMetadataDoesNotCopyPromptContent(t *testing.T) {
	payload := []byte(`{"response_format":{"type":"json_schema","json_schema":{"name":"run_output","strict":true,"schema":{"type":"object","description":"PRIVATE_MATERIAL_DESCRIPTION"}}}}`)
	parameters, _, _, err := summarizePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(parameters)
	if strings.Contains(string(encoded), "PRIVATE_MATERIAL_DESCRIPTION") || !strings.Contains(string(encoded), "schema_hash") {
		t.Fatalf("schema copied into ordinary metadata: %s", encoded)
	}
	if !strings.Contains(string(payload), "PRIVATE_MATERIAL_DESCRIPTION") {
		t.Fatal("capture filtering mutated request")
	}
}
