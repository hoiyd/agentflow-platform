package projectguidance

import (
	"context"
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *Service) Projects(ctx context.Context, workspace string) (domain.ProjectGuidanceCatalog, error) {
	result := domain.ProjectGuidanceCatalog{Configured: s.config != "", Projects: []domain.ProjectGuidanceProject{}}
	if !result.Configured {
		return result, nil
	}
	if err := s.files.ValidateGuidanceConfig(s.config); err != nil {
		return result, problem("project_guidance_unavailable", "Guidance trust configuration is unavailable or unsafe")
	}
	projects, _, err := loadConfig(s.config)
	if err != nil {
		return result, err
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if project.WorkspaceID != workspace {
			continue
		}
		if _, err := s.files.ProjectRootRevision(ctx, workspace, project.Root, nil); err != nil {
			continue // A trust record does not restore revoked filesystem access.
		}
		result.Projects = append(result.Projects, domain.ProjectGuidanceProject{Root: project.Root, Entry: project.Entry, RuleCount: len(project.Rules)})
	}
	sort.Slice(result.Projects, func(i, j int) bool { return result.Projects[i].Root < result.Projects[j].Root })
	return result, nil
}

func (s *Service) Files(ctx context.Context, workspace, root, directory string) (domain.ProjectFilePage, error) {
	if !rootID.MatchString(root) || directory != "." && !validSelectionPath(directory) {
		return domain.ProjectFilePage{}, problem("project_guidance_invalid", "Choose a logical project root and a relative directory")
	}
	if _, _, err := s.trusted(workspace, root); err != nil {
		return domain.ProjectFilePage{}, err
	}
	browser, ok := s.files.(interface {
		ListProjectFiles(context.Context, string, string, string) (domain.ProjectFilePage, error)
	})
	if !ok {
		return domain.ProjectFilePage{}, problem("project_guidance_unavailable", "Project file selection is unavailable")
	}
	page, err := browser.ListProjectFiles(ctx, workspace, root, directory)
	if err != nil {
		return domain.ProjectFilePage{}, problem("project_guidance_unavailable", "Project directory is unavailable or unsafe")
	}
	selectable := page.Entries[:0]
	for _, entry := range page.Entries {
		if validSelectionPath(entry.Path) {
			selectable = append(selectable, entry)
		}
	}
	page.Entries = selectable
	return page, nil
}
