package contexteval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool/artifact"
)

const responseInstructions = `Return JSON only. A fact map has one key per requested field; each value is {"value": typed value, "source_id": historical source ID, "quote": exact complete sentence from that source, including introductory prefixes}. Use integer minor units for amount_minor, boolean for may_release, and strings for other fields. Use the latest explicit historical statement for each fact and quote only its supporting sentence, not surrounding sentences. Preserve exact source quotes and IDs; do not substitute obsolete values. For receipt, report the historical confirmation status: "unknown" means unknown confirmation status, not a missing field; a pending document does not establish confirmation or replace that historical statement. Only fields with no historical evidence use null with empty evidence; never invent evidence.`
const probeSystem = "FIDELITY PROBE: extract current facts from the supplied historical evidence only. This is extraction, not a quality judgment. " + responseInstructions
const continuationSystem = "Continue the user's task using history and required Task State. Read the supporting material with artifact_read before answering. Return {\"facts\": <fact map>, \"citations\": [{\"source_id\": logical material ID, \"quote\": exact evidence sentence}]}. Return exactly one material citation, quoting only the sentence beginning with Evidence: from that material. Historical citations belong in facts, not citations. Do not perform external actions. " + responseInstructions

func factFields(c liveCase) string {
	keys := make([]string, 0, len(c.ExpectedFacts))
	for key := range c.ExpectedFacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	b, _ := json.Marshal(keys)
	return string(b)
}

func checkClaims(text string, c liveCase) LiveCheck {
	result := LiveCheck{Findings: []string{}}
	var claims map[string]liveClaim
	if strictLiveJSON(text, &claims) != nil {
		result.Findings = append(result.Findings, "invalid_fact_json")
		return result
	}
	if len(claims) != len(c.ExpectedFacts) {
		result.Findings = append(result.Findings, "fact_set_mismatch")
	}
	keys := make([]string, 0, len(c.ExpectedFacts))
	for key := range c.ExpectedFacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		want := c.ExpectedFacts[key]
		got, ok := claims[key]
		if !ok || !bytes.Equal(canonicalValue(got.Value), canonicalValue(want.Value)) {
			result.Findings = append(result.Findings, "fact_value:"+key)
		}
		if !ok || got.SourceID != want.SourceID || got.Quote != want.Quote {
			result.Findings = append(result.Findings, "fact_provenance:"+key)
		}
	}
	result.Passed = len(result.Findings) == 0
	return result
}

func checkContinuation(text string, c liveCase, reads []LiveMaterialRead, artifactID string) LiveCheck {
	var answer struct {
		Facts     json.RawMessage `json:"facts"`
		Citations []struct {
			SourceID string `json:"source_id"`
			Quote    string `json:"quote"`
		} `json:"citations"`
	}
	if strictLiveJSON(text, &answer) != nil {
		return LiveCheck{Findings: []string{"invalid_outcome_json"}}
	}
	result := checkClaims(string(answer.Facts), c)
	if len(answer.Citations) != 1 || answer.Citations[0].SourceID != c.Material.ID || answer.Citations[0].Quote != c.Material.RequiredQuote {
		result.Findings = append(result.Findings, "material_evidence_mismatch")
	}
	read := false
	for _, observed := range reads {
		if observed.ArtifactID == artifactID && observed.Complete && observed.Offset == 0 && observed.ContentHash == liveDigest([]byte(c.Material.Content)) {
			read = true
		}
	}
	if !read {
		result.Findings = append(result.Findings, "material_read_not_observed")
	}
	result.Passed = len(result.Findings) == 0
	return result
}

func collectLiveReads(events []domain.RunEvent) []LiveMaterialRead {
	reads := []LiveMaterialRead{}
	for _, e := range events {
		if e.Type != domain.EventToolCompleted || e.Payload["tool_name"] != artifact.ReadToolName || e.Payload["truncated"] == true {
			continue
		}
		b, _ := json.Marshal(e.Payload["result"])
		var observed domain.ToolArtifactRead
		if json.Unmarshal(b, &observed) != nil {
			continue
		}
		call, _ := e.Payload["tool_call_id"].(string)
		reads = append(reads, LiveMaterialRead{EventID: e.ID, CallID: call, ArtifactID: observed.Artifact.ID, ContentHash: liveDigest([]byte(observed.Content)), Offset: observed.Offset, Bytes: len(observed.Content), Complete: observed.Complete})
	}
	return reads
}

func strictLiveJSON(text string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func canonicalValue(b json.RawMessage) []byte {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(&v) != nil {
		return nil
	}
	out, _ := json.Marshal(v)
	return out
}
