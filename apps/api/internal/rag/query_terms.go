package rag

import "strings"

func QueryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && !(r >= '\u4e00' && r <= '\u9fff')
	})
	terms := make([]string, 0, len(fields)+len([]rune(query)))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 || seen[field] {
			continue
		}
		seen[field] = true
		terms = append(terms, field)
	}
	runes := []rune(strings.TrimSpace(query))
	for i := 0; i+1 < len(runes); i++ {
		if !isCJK(runes[i]) || !isCJK(runes[i+1]) {
			continue
		}
		term := string(runes[i : i+2])
		if seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func isCJK(r rune) bool {
	return r >= '\u4e00' && r <= '\u9fff'
}
