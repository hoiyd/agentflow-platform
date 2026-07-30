package rag

import "agentflow-platform/apps/api/internal/domain"

func contextRolePriority(role string) int {
	switch role {
	case domain.ContextRoleMatchedChild:
		return 3
	case domain.ContextRoleParent:
		return 2
	case domain.ContextRoleAdjacent:
		return 1
	default:
		return 0
	}
}

func shouldUseContextRanking(candidate domain.RetrievedDocumentChunk, current domain.RetrievedDocumentChunk) bool {
	candidatePriority := contextRolePriority(candidate.ContextRole)
	currentPriority := contextRolePriority(current.ContextRole)
	if candidatePriority != currentPriority {
		return candidatePriority > currentPriority
	}
	if candidate.RerankRank > 0 && (current.RerankRank == 0 || candidate.RerankRank < current.RerankRank) {
		return true
	}
	return candidate.RerankScore > current.RerankScore
}

func copyContextRanking(target *domain.RetrievedDocumentChunk, source domain.RetrievedDocumentChunk) {
	target.Similarity = source.Similarity
	target.RecencyBoost = source.RecencyBoost
	target.Score = source.Score
	target.VectorRank = source.VectorRank
	target.LexicalRank = source.LexicalRank
	target.LexicalScore = source.LexicalScore
	target.RRFScore = source.RRFScore
	target.FusionRank = source.FusionRank
	target.RerankRank = source.RerankRank
	target.LexicalBoost = source.LexicalBoost
	target.MetadataBoost = source.MetadataBoost
	target.DiversityPenalty = source.DiversityPenalty
	target.RerankScore = source.RerankScore
	target.MatchedTerms = append([]string(nil), source.MatchedTerms...)
	target.EvidenceScore = source.EvidenceScore
	target.EvidenceCoverage = source.EvidenceCoverage
	target.Confidence = source.Confidence
	target.FilterReason = source.FilterReason
	target.ContextRole = source.ContextRole
}
