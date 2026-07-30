package rag

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	ContextSelectionVersion          = "parent-child-v1"
	DefaultKnowledgeContextMaxTokens = 16000
	DefaultContextNeighborWindow     = 1
)

type ContextSelector struct {
	store SearchStore
}

func NewContextSelector(store SearchStore) *ContextSelector {
	return &ContextSelector{store: store}
}

func (s *ContextSelector) Select(search domain.DocumentSearch, matches []domain.RetrievedDocumentChunk) ([]domain.RetrievedDocumentChunk, domain.ContextSelectionInfo, domain.KnowledgeSecurityInfo, error) {
	maxTokens := search.KnowledgeContextMaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultKnowledgeContextMaxTokens
	}
	selection := domain.ContextSelectionInfo{
		Version:       ContextSelectionVersion,
		MaxTokens:     maxTokens,
		ScopeFiltered: true,
	}
	security := domain.KnowledgeSecurityInfo{
		PolicyVersion:    PromptInjectionPolicyVersion,
		UntrustedContext: true,
	}
	if len(matches) == 0 {
		items, transformation := TransformContext(nil, maxTokens)
		selection.Transformation = &transformation
		return items, selection, security, nil
	}
	if s == nil || s.store == nil {
		return nil, selection, security, fmt.Errorf("context selection store is required")
	}

	selected := make([]domain.RetrievedDocumentChunk, 0, len(matches)*2)
	selectedIDs := make(map[string]struct{}, len(matches)*2)
	matchedIDs := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		matchedIDs[match.Chunk.ID] = struct{}{}
	}

	selectedMatches := make([]domain.RetrievedDocumentChunk, 0, len(matches))
	for _, match := range matches {
		documentID := strings.TrimSpace(match.Document.ID)
		if documentID == "" {
			documentID = strings.TrimSpace(match.Chunk.DocumentID)
		}
		if documentID == "" || !contextDocumentMatchesWorkspace(match.Document, search.WorkspaceID) {
			continue
		}

		match.ContextRole = domain.ContextRoleMatchedChild
		match.MatchedChunkID = match.Chunk.ID
		if !addContextItem(&selected, selectedIDs, &selection, match, maxTokens) {
			continue
		}
		selection.MatchedChildren++
		selectedMatches = append(selectedMatches, match)
	}

	for _, match := range selectedMatches {
		documentID := strings.TrimSpace(match.Document.ID)
		if documentID == "" {
			documentID = strings.TrimSpace(match.Chunk.DocumentID)
		}
		scope := domain.DocumentContextSearch{
			DocumentID:     documentID,
			WorkspaceID:    contextWorkspaceID(search.WorkspaceID, match.Document.WorkspaceID),
			ParentID:       strings.TrimSpace(match.Chunk.ParentID),
			ChunkIndex:     match.Chunk.ChunkIndex,
			NeighborWindow: DefaultContextNeighborWindow,
			Metadata:       search.Metadata,
		}
		candidates, err := s.store.ListDocumentContextChunks(scope)
		if err != nil {
			return nil, selection, security, fmt.Errorf("load context for chunk %s: %w", match.Chunk.ID, err)
		}

		expansionCandidates := make([]domain.RetrievedDocumentChunk, 0, len(candidates))
		for _, candidate := range candidates {
			if _, isMatch := matchedIDs[candidate.Chunk.ID]; isMatch {
				continue
			}
			if _, alreadySelected := selectedIDs[candidate.Chunk.ID]; alreadySelected {
				continue
			}
			if !contextCandidateMatchesScope(candidate, scope) {
				continue
			}
			expansionCandidates = append(expansionCandidates, candidate)
		}
		expansionCandidates, expansionSecurity := GuardPromptInjection(expansionCandidates)
		security = mergeKnowledgeSecurity(security, expansionSecurity)

		parentCandidates, adjacentCandidates := orderContextCandidates(match, expansionCandidates, scope.NeighborWindow)
		parentSelected := false
		for _, candidate := range parentCandidates {
			candidate.ContextRole = domain.ContextRoleParent
			candidate.MatchedChunkID = match.Chunk.ID
			if addContextItem(&selected, selectedIDs, &selection, candidate, maxTokens) {
				selection.ParentChunks++
				parentSelected = true
			}
		}
		if parentSelected {
			continue
		}
		for _, candidate := range adjacentCandidates {
			candidate.ContextRole = domain.ContextRoleAdjacent
			candidate.MatchedChunkID = match.Chunk.ID
			if addContextItem(&selected, selectedIDs, &selection, candidate, maxTokens) {
				selection.AdjacentChunks++
			}
		}
	}

	transformed, transformation := TransformContext(selected, maxTokens)
	selection.TokensUsed = contextItemsTokens(transformed)
	selection.Transformation = &transformation
	return transformed, selection, security, nil
}

func addContextItem(items *[]domain.RetrievedDocumentChunk, selectedIDs map[string]struct{}, selection *domain.ContextSelectionInfo, item domain.RetrievedDocumentChunk, maxTokens int) bool {
	if strings.TrimSpace(item.Chunk.ID) == "" {
		return false
	}
	if _, exists := selectedIDs[item.Chunk.ID]; exists {
		return false
	}
	tokens := contextChunkTokens(item.Chunk)
	if selection.TokensUsed+tokens > maxTokens {
		return false
	}
	item.Chunk.TokenCount = tokens
	item.Chunk.Document = item.Document
	selectedIDs[item.Chunk.ID] = struct{}{}
	selection.TokensUsed += tokens
	*items = append(*items, item)
	return true
}

func orderContextCandidates(match domain.RetrievedDocumentChunk, candidates []domain.RetrievedDocumentChunk, neighborWindow int) ([]domain.RetrievedDocumentChunk, []domain.RetrievedDocumentChunk) {
	parent := make([]domain.RetrievedDocumentChunk, 0, len(candidates))
	adjacent := make([]domain.RetrievedDocumentChunk, 0, len(candidates))
	for _, candidate := range candidates {
		if match.Chunk.ParentID != "" && candidate.Chunk.ParentID == match.Chunk.ParentID {
			parent = append(parent, candidate)
		}
		if neighborWindow > 0 && abs(candidate.Chunk.ChunkIndex-match.Chunk.ChunkIndex) <= neighborWindow {
			adjacent = append(adjacent, candidate)
		}
	}
	sortByDistance(parent, match.Chunk.ChunkIndex)
	sortByDistance(adjacent, match.Chunk.ChunkIndex)
	return parent, adjacent
}

func sortByDistance(items []domain.RetrievedDocumentChunk, chunkIndex int) {
	sort.SliceStable(items, func(i, j int) bool {
		iDistance := abs(items[i].Chunk.ChunkIndex - chunkIndex)
		jDistance := abs(items[j].Chunk.ChunkIndex - chunkIndex)
		if iDistance != jDistance {
			return iDistance < jDistance
		}
		if items[i].Chunk.ChunkIndex != items[j].Chunk.ChunkIndex {
			return items[i].Chunk.ChunkIndex < items[j].Chunk.ChunkIndex
		}
		return items[i].Chunk.ID < items[j].Chunk.ID
	})
}

func contextCandidateMatchesScope(candidate domain.RetrievedDocumentChunk, scope domain.DocumentContextSearch) bool {
	documentID := candidate.Document.ID
	if documentID == "" {
		documentID = candidate.Chunk.DocumentID
	}
	if documentID != scope.DocumentID || !contextDocumentMatchesWorkspace(candidate.Document, scope.WorkspaceID) {
		return false
	}
	for key, expected := range scope.Metadata {
		value, ok := candidate.Chunk.Metadata[key]
		if !ok {
			value, ok = candidate.Document.Metadata[key]
		}
		if !ok || strings.TrimSpace(fmt.Sprint(value)) != strings.TrimSpace(expected) {
			return false
		}
	}
	return true
}

func contextDocumentMatchesWorkspace(document domain.Document, workspaceID string) bool {
	return strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(document.WorkspaceID) == strings.TrimSpace(workspaceID)
}

func contextWorkspaceID(requested string, matched string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	return strings.TrimSpace(matched)
}

func contextChunkTokens(chunk domain.DocumentChunk) int {
	if chunk.TokenCount > 0 {
		return chunk.TokenCount
	}
	runes := utf8.RuneCountInString(strings.TrimSpace(chunk.Content))
	if runes == 0 {
		return 1
	}
	return (runes + 3) / 4
}

func mergeKnowledgeSecurity(primary domain.KnowledgeSecurityInfo, secondary domain.KnowledgeSecurityInfo) domain.KnowledgeSecurityInfo {
	if primary.PolicyVersion == "" {
		primary.PolicyVersion = secondary.PolicyVersion
	}
	primary.UntrustedContext = primary.UntrustedContext || secondary.UntrustedContext
	primary.CheckedCandidates += secondary.CheckedCandidates
	primary.BlockedCandidates += secondary.BlockedCandidates
	primary.Decisions = append(primary.Decisions, secondary.Decisions...)
	return primary
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
