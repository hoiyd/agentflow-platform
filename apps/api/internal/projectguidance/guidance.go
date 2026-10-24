package projectguidance

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/redaction"
)

// Files reuses TOOL-029's current Workspace/root authorization and no-follow
// text reader. Reading permission alone does not establish instruction trust.
type Files interface {
	ValidateGuidanceConfig(string) error
	ProjectRootRevision(context.Context, string, string, []string) (string, error)
	ReadProjectText(context.Context, string, string, string, int) (string, error)
}

type Service struct {
	config string
	files  Files
}

func New(config string, files Files) (*Service, error) {
	if files == nil {
		return nil, problem("project_guidance_unavailable", "Project guidance requires scoped file access")
	}
	if config != "" {
		if err := files.ValidateGuidanceConfig(config); err != nil {
			return nil, problem("project_guidance_invalid", "Guidance trust configuration must be operator-controlled")
		}
		if _, _, err := loadConfig(config); err != nil {
			return nil, err
		}
	}
	return &Service{config: config, files: files}, nil
}

func (s *Service) trusted(workspace, root string) (project, string, error) {
	if s.files.ValidateGuidanceConfig(s.config) != nil {
		return project{}, "", problem("project_guidance_unavailable", "Guidance trust configuration is unavailable or unsafe")
	}
	items, revision, err := loadConfig(s.config)
	if err != nil {
		return project{}, "", err
	}
	for _, item := range items {
		if item.WorkspaceID == workspace && item.Root == root {
			return item, revision, nil
		}
	}
	return project{}, "", problem("project_guidance_untrusted", "Project guidance is not trusted for this Workspace/root")
}

func NormalizeSelection(selection domain.ProjectContext) (domain.ProjectContext, error) {
	if !rootID.MatchString(selection.Root) || len(selection.Paths) == 0 || len(selection.Paths) > 16 {
		return domain.ProjectContext{}, problem("project_guidance_invalid", "Project context requires one logical root and 1-16 existing relative files")
	}
	selection.Paths = slices.Clone(selection.Paths)
	for _, name := range selection.Paths {
		if !validPath(name) || strings.ContainsAny(name, "*?[]") {
			return domain.ProjectContext{}, problem("project_guidance_invalid", "Project context accepts concrete relative files, not patterns or directories")
		}
	}
	slices.Sort(selection.Paths)
	selection.Paths = slices.Compact(selection.Paths)
	return selection, nil
}

func (s *Service) Freeze(ctx context.Context, workspace string, selection domain.ProjectContext) (*domain.ProjectGuidanceSnapshot, error) {
	selection, err := NormalizeSelection(selection)
	if err != nil {
		return nil, err
	}
	project, configRevision, err := s.trusted(workspace, selection.Root)
	if err != nil {
		return nil, err
	}
	rootRevision, err := s.files.ProjectRootRevision(ctx, workspace, selection.Root, selection.Paths)
	if err != nil {
		return nil, problem("project_guidance_unavailable", "Project root or selected files are not currently authorized")
	}
	frozen := &domain.ProjectGuidanceSnapshot{Version: Version, SourceType: SourceType, WorkspaceID: workspace, Selection: selection, RootRevision: rootRevision, ConfigRevision: configRevision}
	entries := []domain.ProjectGuidanceEntry{{Path: project.Entry, Reason: "project_entry", MatchedPaths: slices.Clone(selection.Paths)}}
	for _, rule := range project.Rules {
		matched := matches(rule.Paths, selection.Paths)
		reason := "path_match"
		if len(matched) == 0 {
			reason = "path_not_matched"
		}
		entries = append(entries, domain.ProjectGuidanceEntry{Path: rule.Path, Patterns: slices.Clone(rule.Paths), MatchedPaths: matched, Reason: reason})
	}
	seen := map[string]bool{}
	bytes, tokens := 0, 0
	for _, entry := range entries {
		if entry.Reason != "path_not_matched" {
			content, err := s.files.ReadProjectText(ctx, workspace, selection.Root, entry.Path, MaxFileBytes)
			if err != nil {
				if errors.Is(err, redaction.ErrCredentialContent) {
					return nil, problem("project_guidance_sensitive", "Guidance contains credential-shaped content; operator review is required")
				}
				if failure.Describe(err).Code == "filesystem_file_limit" {
					return nil, problem("project_guidance_budget_exceeded", "Guidance file exceeds the bounded text read limit")
				}
				return nil, problem("project_guidance_unavailable", "Trusted guidance text is unavailable or unsafe")
			}
			if !utf8.ValidString(content) || strings.ContainsRune(content, 0) || strings.TrimSpace(content) == "" {
				return nil, problem("project_guidance_invalid", "Guidance requires nonempty UTF-8 text")
			}
			if redaction.ValidateValue(content) != nil {
				return nil, problem("project_guidance_sensitive", "Guidance contains credential-shaped content; operator review is required")
			}
			entry.Hash, entry.OriginalBytes = hash(content), len(content)
			bytes += len(content)
			if seen[strings.TrimSpace(content)] {
				entry.Reason = "duplicate_content"
			} else {
				seen[strings.TrimSpace(content)] = true
				entry.Content = content
				entry.EstimatedTokens = contextassembly.EstimateTokens(content)
				tokens += entry.EstimatedTokens
			}
			if len(content) > MaxFileBytes || bytes > MaxTotalBytes || tokens > MaxTokens {
				return nil, &Error{code: "project_guidance_budget_exceeded", message: "Project guidance exceeds its bounded content budget; shorten the trusted files", details: map[string]any{"path": entry.Path, "observed_bytes": bytes, "estimated_tokens": tokens, "max_file_bytes": MaxFileBytes, "max_total_bytes": MaxTotalBytes, "max_tokens": MaxTokens}}
			}
		}
		frozen.Entries = append(frozen.Entries, entry)
	}
	return frozen, nil
}

func matches(patterns, paths []string) []string {
	matched := []string{}
	for _, name := range paths {
		for _, pattern := range patterns {
			if ok, _ := path.Match(pattern, name); ok {
				matched = append(matched, name)
				break
			}
		}
	}
	return matched
}
