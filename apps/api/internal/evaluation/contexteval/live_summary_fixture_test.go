package contexteval

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Build genuine evidence against the actual selected message IDs on the wire.
// Truth labels are used only by the model fixture and never enter the request.
func liveFixtureSummary(t *testing.T, prompt, facts, prose string) string {
	t.Helper()
	ids := map[string]string{}
	for _, match := range regexp.MustCompile(`--- message_id=(\S+) role=user ---\nsource_id=(\S+)`).FindAllStringSubmatch(prompt, -1) {
		ids[match[2]] = match[1]
	}
	var claims map[string]liveClaim
	if err := json.Unmarshal([]byte(facts), &claims); err != nil {
		t.Fatal(err)
	}
	evidence := []map[string]string{}
	seen := map[string]bool{}
	keys := make([]string, 0, len(claims))
	for key := range claims {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		claim := claims[key]
		identity := claim.SourceID + claim.Quote
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if ids[claim.SourceID] == "" || !strings.Contains(prompt, claim.Quote) {
			t.Error("fixture evidence absent from actual compaction input")
		}
		evidence = append(evidence, map[string]string{"message_id": ids[claim.SourceID], "role": "user", "quote": claim.Quote})
	}
	return "## Source Evidence\n" + mustLiveJSON(evidence) + "\n\n## Established Facts\n" + prose
}

func liveFixturePhysicalIDs(t *testing.T, prompt, facts string) string {
	t.Helper()
	_, block, ok := strings.Cut(prompt, "## Source Evidence\n")
	if !ok {
		return facts
	} // The full-history arm uses logical labels in the raw source.
	var evidence []struct {
		MessageID string `json:"message_id"`
		Quote     string `json:"quote"`
	}
	if err := json.NewDecoder(strings.NewReader(block)).Decode(&evidence); err != nil {
		t.Error(err)
		return facts
	}
	var claims map[string]liveClaim
	if err := json.Unmarshal([]byte(facts), &claims); err != nil {
		t.Error(err)
		return facts
	}
	for key, claim := range claims {
		found := false
		for _, source := range evidence {
			if source.Quote == claim.Quote {
				claim.SourceID = source.MessageID
				found = true
				break
			}
		}
		if !found {
			t.Error("original quote missing from actual candidate input")
		}
		claims[key] = claim
	}
	return mustLiveJSON(claims)
}
