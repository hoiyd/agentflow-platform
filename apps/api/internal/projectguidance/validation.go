package projectguidance

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/redaction"
)

func ValidateSnapshot(frozen *domain.ProjectGuidanceSnapshot) error {
	if frozen == nil {
		return nil
	}
	selection, err := NormalizeSelection(frozen.Selection)
	if err != nil || !slices.Equal(selection.Paths, frozen.Selection.Paths) || frozen.Version != Version || frozen.SourceType != SourceType || frozen.WorkspaceID == "" || frozen.RootRevision == "" || frozen.ConfigRevision == "" || len(frozen.Entries) == 0 || len(frozen.Entries) > 17 {
		return problem("project_guidance_invalid", "Frozen project instructions identity is invalid")
	}
	bytes, tokens := 0, 0
	seen := map[string]bool{}
	for index, entry := range frozen.Entries {
		if !validPath(entry.Path) || seen[entry.Path] || entry.OriginalBytes < 0 || entry.OriginalBytes > MaxFileBytes {
			return problem("project_guidance_invalid", "Frozen instruction source is invalid")
		}
		seen[entry.Path] = true
		for _, pattern := range entry.Patterns {
			if !validPattern(pattern) {
				return problem("project_guidance_invalid", "Frozen instruction pattern is invalid")
			}
		}
		matched := matches(entry.Patterns, selection.Paths)
		if index == 0 {
			matched = selection.Paths
		}
		if !slices.Equal(matched, entry.MatchedPaths) {
			return problem("project_guidance_invalid", "Frozen instruction path matches are invalid")
		}
		switch entry.Reason {
		case "project_entry", "path_match":
			if (entry.Reason == "project_entry") != (index == 0) || len(matched) == 0 || strings.TrimSpace(entry.Content) == "" || hash(entry.Content) != entry.Hash || len(entry.Content) != entry.OriginalBytes || contextassembly.EstimateTokens(entry.Content) != entry.EstimatedTokens || redaction.ValidateValue(entry.Content) != nil {
				return problem("project_guidance_invalid", "Frozen instruction content or hash is invalid")
			}
		case "path_not_matched":
			if len(matched) != 0 || entry.Content != "" || entry.Hash != "" || entry.OriginalBytes != 0 || entry.EstimatedTokens != 0 {
				return problem("project_guidance_invalid", "Unmatched instruction sources cannot contain instruction text")
			}
		case "duplicate_content":
			decoded, err := hex.DecodeString(entry.Hash)
			if index == 0 || err != nil || len(decoded) != 32 || len(matched) == 0 || entry.Content != "" || entry.EstimatedTokens != 0 {
				return problem("project_guidance_invalid", "Duplicate instruction record is invalid")
			}
		default:
			return problem("project_guidance_invalid", "Frozen instruction selection reason is invalid")
		}
		bytes += entry.OriginalBytes
		tokens += entry.EstimatedTokens
	}
	if bytes > MaxTotalBytes || tokens > MaxTokens {
		return problem("project_guidance_budget_exceeded", "Frozen instructions exceed their content budget")
	}
	return nil
}

// Recheck authority, not live file contents. Changing text affects new Runs;
// removing the root, entry, or any previously matched rule blocks continuation.
func (s *Service) Validate(ctx context.Context, workspace string, frozen *domain.ProjectGuidanceSnapshot) error {
	if err := ValidateSnapshot(frozen); err != nil || frozen == nil {
		return err
	}
	if frozen.WorkspaceID != workspace {
		return problem("project_guidance_unavailable", "Frozen instructions do not belong to this Workspace")
	}
	project, _, err := s.trusted(workspace, frozen.Selection.Root)
	if err != nil {
		return err
	}
	revision, err := s.files.ProjectRootRevision(ctx, workspace, frozen.Selection.Root, frozen.Selection.Paths)
	if err != nil || revision != frozen.RootRevision {
		return problem("project_guidance_unavailable", "Source root changed or was revoked; a new authorized Run is required")
	}
	for index, entry := range frozen.Entries {
		if index == 0 {
			if entry.Path != project.Entry {
				return problem("project_guidance_untrusted", "Frozen instruction entry is no longer trusted")
			}
			continue
		}
		if entry.Reason == "path_not_matched" {
			continue
		}
		found := false
		for _, rule := range project.Rules {
			if rule.Path == entry.Path && slices.Equal(rule.Paths, entry.Patterns) {
				found = true
				break
			}
		}
		if !found {
			return problem("project_guidance_untrusted", "Frozen instruction trust was revoked; operator review is required")
		}
	}
	return nil
}
