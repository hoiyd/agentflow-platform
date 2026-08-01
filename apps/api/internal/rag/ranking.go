package rag

func NormalizeSearchLimit(limit int) int {
	if limit <= 0 {
		return 5
	}
	if limit > 10 {
		return 10
	}
	return limit
}

func CandidateLimit(limit int) int {
	if limit <= 0 {
		limit = 5
	}
	candidateLimit := limit * 4
	if candidateLimit < 10 {
		candidateLimit = 10
	}
	if candidateLimit > 20 {
		candidateLimit = 20
	}
	return candidateLimit
}
