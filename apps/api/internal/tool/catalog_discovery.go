package tool

import (
	"encoding/json"
	"sort"
	"strings"
	"unicode"
)

type DiscoveryMatch struct {
	Name         string   `json:"name"`
	Toolset      string   `json:"toolset"`
	Revision     string   `json:"revision"`
	Source       string   `json:"source"`
	MatchedTerms []string `json:"matched_terms"`
}

type discoveryEntry struct {
	match DiscoveryMatch
	text  string
}

// DiscoveryIndex belongs to one already-filtered Catalog. Toolsets are naming
// groups, not an allowlist or a mechanism for granting a whole group.
type DiscoveryIndex struct {
	entries []discoveryEntry
}

func (c *Catalog) DiscoveryIndex() DiscoveryIndex {
	index := DiscoveryIndex{}
	for _, name := range c.EnabledNames() {
		binding, _ := c.ResolveReady(name)
		descriptor := binding.Descriptor
		group, _, _ := strings.Cut(name, "_")
		parameters, _ := json.Marshal(descriptor.Parameters)
		index.entries = append(index.entries, discoveryEntry{
			match: DiscoveryMatch{Name: name, Toolset: group, Revision: descriptor.DefinitionRevision, Source: string(descriptor.Security.Source)},
			text:  strings.ToLower(name + " " + descriptor.Description + " " + string(parameters)),
		})
	}
	return index
}

func (index DiscoveryIndex) Search(query string, limit int) []DiscoveryMatch {
	terms := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	type scored struct {
		match DiscoveryMatch
		score int
	}
	matches := []scored{}
	for _, entry := range index.entries {
		item := scored{match: entry.match}
		seen := map[string]bool{}
		for _, term := range terms {
			if !seen[term] && strings.Contains(entry.text, term) {
				item.score++
				if strings.Contains(entry.match.Name, term) {
					item.score += 4
				}
				item.match.MatchedTerms = append(item.match.MatchedTerms, term)
				seen[term] = true
			}
		}
		if item.score > 0 {
			matches = append(matches, item)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].match.Name < matches[j].match.Name
	})
	result := []DiscoveryMatch{}
	for i := 0; i < len(matches) && i < limit; i++ {
		result = append(result, matches[i].match)
	}
	return result
}

// Summary bounds discovery metadata independently of the full Schema cost.
// Search still spans all authorized candidates when this index is abbreviated.
func (index DiscoveryIndex) Summary(maxBytes int) string {
	const abbreviated = "Index abbreviated; search also covers unlisted authorized tools.\n"
	if maxBytes < len(abbreviated) {
		return ""
	}
	var summary strings.Builder
	for _, entry := range index.entries {
		line := entry.match.Toolset + ": " + entry.match.Name + "\n"
		if summary.Len()+len(line)+len(abbreviated) > maxBytes {
			summary.WriteString(abbreviated)
			break
		}
		summary.WriteString(line)
	}
	return summary.String()
}
